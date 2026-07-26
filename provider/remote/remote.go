package remote

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"io"
	"net"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/sagernet/sing-box/adapter"
	adapterProvider "github.com/sagernet/sing-box/adapter/provider"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing-box/provider/parser"
	"github.com/sagernet/sing/common"
	E "github.com/sagernet/sing/common/exceptions"
	F "github.com/sagernet/sing/common/format"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/common/ntp"
	"github.com/sagernet/sing/service"
)

const (
	providerRequestTimeout  = 30 * time.Second
	maxProviderResponseSize = 16 << 20
)

func RegisterProvider(registry *adapterProvider.Registry) {
	adapterProvider.Register[option.ProviderRemoteOptions](registry, C.ProviderTypeRemote, NewProviderRemote)
}

var (
	_ adapter.Provider                 = (*ProviderRemote)(nil)
	_ adapter.ProviderUpdater          = (*ProviderRemote)(nil)
	_ adapter.ProviderSubscriptionInfo = (*ProviderRemote)(nil)
)

type ProviderRemote struct {
	adapterProvider.Adapter
	ctx          context.Context
	cancel       context.CancelFunc
	logger       log.ContextLogger
	outbound     adapter.OutboundManager
	cacheFile    adapter.CacheFile
	cacheKey     string
	dialer       N.Dialer
	tickerAccess sync.Mutex
	ticker       *time.Ticker
	fetchAccess  sync.Mutex

	stateAccess      sync.RWMutex
	lastEtag         string
	lastOutOpts      []option.Outbound
	lastUpdated      time.Time
	subscriptionInfo adapter.SubscriptionInfo

	url               string
	userAgent         string
	downloadDetour    string
	updateInterval    time.Duration
	disableAutoUpdate bool
	exclude           *regexp.Regexp
	include           *regexp.Regexp
}

func NewProviderRemote(ctx context.Context, router adapter.Router, logFactory log.Factory, tag string, options option.ProviderRemoteOptions) (adapter.Provider, error) {
	if options.URL == "" {
		return nil, E.New("provider URL is required")
	}
	updateInterval := time.Duration(options.UpdateInterval)
	if updateInterval <= 0 {
		updateInterval = 24 * time.Hour
	}
	if updateInterval < time.Minute {
		updateInterval = time.Minute
	}
	userAgent := options.UserAgent
	if userAgent == "" {
		userAgent = "sing-box " + C.Version
	}
	ctx, cancel := context.WithCancel(ctx)
	outboundManager := service.FromContext[adapter.OutboundManager](ctx)
	if outboundManager == nil {
		cancel()
		return nil, E.New("missing outbound manager")
	}
	logger := logFactory.NewLogger(F.ToString("provider/remote[", tag, "]"))
	return &ProviderRemote{
		Adapter:           adapterProvider.NewAdapter(ctx, router, outboundManager, logFactory, logger, tag, C.ProviderTypeRemote, options.HealthCheck),
		ctx:               ctx,
		cancel:            cancel,
		logger:            logger,
		outbound:          outboundManager,
		cacheKey:          providerCacheKey(tag, options.URL),
		url:               options.URL,
		userAgent:         userAgent,
		downloadDetour:    options.DownloadDetour,
		updateInterval:    updateInterval,
		disableAutoUpdate: options.DisableAutoUpdate,
		exclude:           (*regexp.Regexp)(options.Exclude),
		include:           (*regexp.Regexp)(options.Include),
	}, nil
}

func (s *ProviderRemote) Start() error {
	s.cacheFile = service.FromContext[adapter.CacheFile](s.ctx)
	loadedCache := false
	if s.cacheFile != nil {
		if savedSubscription := s.cacheFile.LoadSubscription(s.cacheKey); savedSubscription != nil {
			if err := s.restoreCache(savedSubscription); err != nil {
				s.logger.Warn(E.Cause(err, "restore cached outbound provider"))
			} else {
				loadedCache = true
				s.UpdateGroups()
			}
		}
	}

	if s.downloadDetour != "" {
		detour, loaded := s.outbound.Outbound(s.downloadDetour)
		if !loaded {
			return E.New("detour outbound not found: ", s.downloadDetour)
		}
		s.dialer = detour
	} else {
		s.dialer = s.outbound.Default()
	}
	if s.dialer == nil {
		return E.New("missing download dialer")
	}

	lastUpdated := s.UpdatedAt()
	if !loadedCache {
		if err := s.fetch(s.ctx); err != nil {
			return E.Cause(err, "initial outbound provider update")
		}
	} else if !s.disableAutoUpdate && time.Since(lastUpdated) >= s.updateInterval {
		if err := s.fetch(s.ctx); err != nil {
			s.logger.Warn(E.Cause(err, "refresh cached outbound provider"))
		}
	}
	if err := s.Adapter.Start(); err != nil {
		return err
	}
	if !s.disableAutoUpdate {
		go s.loopUpdate()
	}
	return nil
}

func (s *ProviderRemote) restoreCache(savedSubscription *adapter.SavedBinary) error {
	content := string(savedSubscription.Content)
	firstLine, remaining := getFirstLine(content)
	var info adapter.SubscriptionInfo
	if parsedInfo, loaded := parseInfo(firstLine); loaded {
		info = parsedInfo
		content = remaining
	}
	if err := s.updateProviderFromContent(content); err != nil {
		return err
	}
	s.stateAccess.Lock()
	s.subscriptionInfo = info
	s.lastUpdated = savedSubscription.LastUpdated
	s.lastEtag = savedSubscription.LastEtag
	s.stateAccess.Unlock()
	return nil
}

func (s *ProviderRemote) Update() error {
	s.tickerAccess.Lock()
	if s.ticker != nil {
		s.ticker.Reset(s.updateInterval)
	}
	s.tickerAccess.Unlock()
	return s.fetch(s.ctx)
}

func (s *ProviderRemote) UpdatedAt() time.Time {
	s.stateAccess.RLock()
	defer s.stateAccess.RUnlock()
	return s.lastUpdated
}

func (s *ProviderRemote) SubscriptionInfo() adapter.SubscriptionInfo {
	s.stateAccess.RLock()
	defer s.stateAccess.RUnlock()
	return s.subscriptionInfo
}

func (s *ProviderRemote) Close() error {
	s.cancel()
	s.tickerAccess.Lock()
	if s.ticker != nil {
		s.ticker.Stop()
		s.ticker = nil
	}
	s.tickerAccess.Unlock()

	// Wait for an in-flight download/parse/update transaction before removing
	// its dynamic outbounds. Otherwise a nearly completed fetch could recreate
	// provider nodes after Close has already removed them.
	s.fetchAccess.Lock()
	s.fetchAccess.Unlock()
	return common.Close(&s.Adapter)
}

func (s *ProviderRemote) fetch(ctx context.Context) error {
	if !s.fetchAccess.TryLock() {
		return E.New("provider is updating")
	}
	defer s.fetchAccess.Unlock()
	if err := s.ctx.Err(); err != nil {
		return err
	}

	requestContext, cancel := context.WithTimeout(ctx, providerRequestTimeout)
	defer cancel()

	s.logger.Debug("updating outbound provider ", s.Tag(), " from URL: ", s.url)
	transport := &http.Transport{
		ForceAttemptHTTP2:   true,
		TLSHandshakeTimeout: C.TCPTimeout,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			return s.dialer.DialContext(ctx, network, M.ParseSocksaddr(address))
		},
		TLSClientConfig: &tls.Config{
			Time: ntp.TimeFuncFromContext(ctx),
		},
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: providerRequestTimeout}

	s.stateAccess.RLock()
	lastEtag := s.lastEtag
	s.stateAccess.RUnlock()

	var response *http.Response
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		request, requestErr := http.NewRequestWithContext(requestContext, http.MethodGet, s.url, nil)
		if requestErr != nil {
			return requestErr
		}
		if lastEtag != "" {
			request.Header.Set("If-None-Match", lastEtag)
		}
		request.Header.Set("User-Agent", s.userAgent)
		response, err = client.Do(request)
		if err == nil {
			break
		}
		if requestContext.Err() != nil {
			return requestContext.Err()
		}
		if attempt < 2 {
			select {
			case <-requestContext.Done():
				return requestContext.Err()
			case <-time.After(time.Second):
			}
		}
	}
	if err != nil {
		return err
	}
	if response == nil {
		return E.New("empty provider response")
	}
	defer response.Body.Close()

	infoString := response.Header.Get("subscription-userinfo")
	info, hasInfo := parseInfo(infoString)
	if response.StatusCode == http.StatusNotModified {
		now := time.Now()
		s.stateAccess.Lock()
		if hasInfo {
			s.subscriptionInfo = info
		}
		s.lastUpdated = now
		s.stateAccess.Unlock()
		if s.cacheFile != nil {
			savedSubscription := s.cacheFile.LoadSubscription(s.cacheKey)
			if savedSubscription != nil {
				if hasInfo {
					separator := bytes.IndexByte(savedSubscription.Content, '\n')
					if separator >= 0 {
						savedSubscription.Content = append([]byte(infoString+"\n"), savedSubscription.Content[separator+1:]...)
					} else {
						savedSubscription.Content = append([]byte(infoString+"\n"), savedSubscription.Content...)
					}
				}
				savedSubscription.LastUpdated = now
				if err := s.cacheFile.SaveSubscription(s.cacheKey, savedSubscription); err != nil {
					s.logger.Error("save outbound provider cache file: ", err)
				}
			}
		}
		s.logger.Info("update outbound provider ", s.Tag(), ": not modified")
		return nil
	}
	if response.StatusCode != http.StatusOK {
		return E.New("unexpected status: ", response.Status)
	}

	contentRaw, err := readProviderResponse(response.Body, response.ContentLength)
	if err != nil {
		return err
	}
	content, _ := parser.DecodeBase64URLSafe(string(contentRaw))
	if !hasInfo {
		firstLine, remaining := getFirstLine(content)
		if parsedInfo, loaded := parseInfo(firstLine); loaded {
			info, hasInfo = parsedInfo, true
			infoString = firstLine
			content, _ = parser.DecodeBase64URLSafe(remaining)
		}
	}
	if err := s.updateProviderFromContent(content); err != nil {
		return err
	}

	now := time.Now()
	etag := response.Header.Get("Etag")
	s.stateAccess.Lock()
	s.lastEtag = etag
	if hasInfo {
		s.subscriptionInfo = info
	}
	s.lastUpdated = now
	lastEtag = s.lastEtag
	s.stateAccess.Unlock()

	if s.cacheFile != nil {
		cacheContent := []byte(content)
		if hasInfo {
			cacheContent = append([]byte(infoString+"\n"), cacheContent...)
		}
		if err := s.cacheFile.SaveSubscription(s.cacheKey, &adapter.SavedBinary{
			Content:     cacheContent,
			LastUpdated: now,
			LastEtag:    lastEtag,
		}); err != nil {
			s.logger.Error("save outbound provider cache file: ", err)
		}
	}
	s.logger.Info("updated outbound provider ", s.Tag())
	return nil
}

func readProviderResponse(reader io.Reader, contentLength int64) ([]byte, error) {
	if contentLength > maxProviderResponseSize {
		return nil, E.New("provider response exceeds 16 MiB")
	}
	content, err := io.ReadAll(io.LimitReader(reader, maxProviderResponseSize+1))
	if err != nil {
		return nil, err
	}
	if len(content) > maxProviderResponseSize {
		return nil, E.New("provider response exceeds 16 MiB")
	}
	return content, nil
}

func providerCacheKey(tag string, rawURL string) string {
	sum := sha256.Sum256([]byte(rawURL))
	return F.ToString(tag, "#", hex.EncodeToString(sum[:]))
}

func (s *ProviderRemote) loopUpdate() {
	ticker := time.NewTicker(s.updateInterval)
	s.tickerAccess.Lock()
	s.ticker = ticker
	s.tickerAccess.Unlock()
	defer func() {
		ticker.Stop()
		s.tickerAccess.Lock()
		if s.ticker == ticker {
			s.ticker = nil
		}
		s.tickerAccess.Unlock()
	}()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			if err := s.fetch(s.ctx); err != nil {
				s.logger.Error("update outbound provider: ", err)
			}
		}
	}
}

func (s *ProviderRemote) updateProviderFromContent(content string) error {
	outboundOptions, err := parser.ParseSubscription(s.ctx, content)
	if err != nil {
		return err
	}
	outboundOptions = common.Filter(outboundOptions, func(outbound option.Outbound) bool {
		return (s.exclude == nil || !s.exclude.MatchString(outbound.Tag)) && (s.include == nil || s.include.MatchString(outbound.Tag))
	})
	if err := parser.ValidateProviderOutbounds(outboundOptions); err != nil {
		return err
	}
	parser.NormalizeProviderTags(outboundOptions)

	s.stateAccess.RLock()
	oldOptions := append([]option.Outbound(nil), s.lastOutOpts...)
	s.stateAccess.RUnlock()
	effectiveOptions, updateErr := s.UpdateOutbounds(oldOptions, outboundOptions)
	s.stateAccess.Lock()
	s.lastOutOpts = effectiveOptions
	s.stateAccess.Unlock()
	s.UpdateGroups()
	return updateErr
}

func getFirstLine(content string) (string, string) {
	lines := strings.SplitN(content, "\n", 2)
	if len(lines) == 1 {
		return lines[0], ""
	}
	return lines[0], lines[1]
}

func parseInfo(infoString string) (adapter.SubscriptionInfo, bool) {
	info := adapter.SubscriptionInfo{}
	if infoString == "" {
		return info, false
	}
	matches := regexp.MustCompile(`(upload|download|total|expire)[\s\t]*=[\s\t]*(-?\d*);?`).FindAllStringSubmatch(infoString, 4)
	if len(matches) == 0 {
		return info, false
	}
	for _, match := range matches {
		switch match[1] {
		case "upload":
			info.Upload = parser.StringToType[int64](match[2])
		case "download":
			info.Download = parser.StringToType[int64](match[2])
		case "total":
			info.Total = parser.StringToType[int64](match[2])
		case "expire":
			info.Expire = parser.StringToType[int64](match[2])
		}
	}
	return info, true
}
