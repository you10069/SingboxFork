package remote

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"io"
	"net"
	"net/http"
	"net/url"
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
	providerRequestTimeout   = 30 * time.Second
	providerUpdateRetryBase  = time.Minute
	providerUpdateRetryLimit = 30 * time.Minute
	maxProviderResponseSize  = 16 << 20
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
	ctx         context.Context
	cancel      context.CancelFunc
	logger      log.ContextLogger
	outbound    adapter.OutboundManager
	cacheFile   adapter.CacheFile
	cacheKey    string
	fetchAccess sync.Mutex

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
		Adapter:           adapterProvider.NewAdapter(ctx, router, outboundManager, logFactory, logger, tag, C.ProviderTypeRemote, options.HealthCheck, options.AdditionalPrefix, options.AdditionalSuffix),
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
			}
		}
	}

	if _, err := s.downloadDialer(); err != nil {
		return err
	}

	lastUpdated := s.UpdatedAt()
	initialUpdateDelay := time.Duration(-1)
	if !loadedCache {
		if err := s.fetch(s.ctx); err != nil {
			return E.Cause(err, "initial outbound provider update")
		}
	} else if !s.disableAutoUpdate && time.Since(lastUpdated) >= s.updateInterval {
		if err := s.fetch(s.ctx); err != nil {
			s.logger.Warn(E.Cause(err, "refresh cached outbound provider"))
			initialUpdateDelay = providerUpdateRetryDelay(s.updateInterval, 1)
		}
	}
	if err := s.Adapter.Start(); err != nil {
		return err
	}
	if !s.disableAutoUpdate {
		if initialUpdateDelay < 0 {
			initialUpdateDelay = s.nextUpdateDelay()
		}
		go s.loopUpdate(initialUpdateDelay)
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
	update, outboundOptions, err := s.prepareProviderFromContent(content)
	if err != nil {
		return err
	}
	if _, err = update.Commit(nil); err != nil {
		return err
	}
	s.stateAccess.Lock()
	s.lastOutOpts = outboundOptions
	s.subscriptionInfo = info
	s.lastUpdated = savedSubscription.LastUpdated
	s.lastEtag = savedSubscription.LastEtag
	s.stateAccess.Unlock()
	return nil
}

func (s *ProviderRemote) Update() error {
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

	// Wait for an in-flight download/parse/update transaction before removing
	// its dynamic outbounds. Otherwise a nearly completed fetch could recreate
	// provider nodes after Close has already removed them.
	s.fetchAccess.Lock()
	s.fetchAccess.Unlock()
	return common.Close(&s.Adapter)
}

func (s *ProviderRemote) downloadDialer() (N.Dialer, error) {
	if s.downloadDetour != "" {
		detour, loaded := s.outbound.Outbound(s.downloadDetour)
		if !loaded || detour == nil {
			return nil, E.New("detour outbound not found: ", s.downloadDetour)
		}
		return detour, nil
	}
	dialer := s.outbound.Default()
	if dialer == nil {
		return nil, E.New("missing download dialer")
	}
	return dialer, nil
}

func (s *ProviderRemote) dialDownloadContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	dialer, err := s.downloadDialer()
	if err != nil {
		return nil, err
	}
	return dialer.DialContext(ctx, network, destination)
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

	s.logger.Debug("updating outbound provider ", s.Tag())
	transport := &http.Transport{
		ForceAttemptHTTP2:   true,
		TLSHandshakeTimeout: C.TCPTimeout,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			return s.dialDownloadContext(ctx, network, M.ParseSocksaddr(address))
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
			return E.Cause(scrubProviderURLError(requestErr), "create provider request")
		}
		if lastEtag != "" {
			request.Header.Set("If-None-Match", lastEtag)
		}
		request.Header.Set("User-Agent", s.userAgent)
		response, err = client.Do(request)
		if err == nil {
			break
		}
		if response != nil {
			_ = response.Body.Close()
			response = nil
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
		return scrubProviderURLError(err)
	}
	if response == nil {
		return E.New("empty provider response")
	}
	defer response.Body.Close()

	infoString := response.Header.Get("subscription-userinfo")
	info, hasInfo := parseInfo(infoString)
	if response.StatusCode == http.StatusNotModified {
		if len(s.Outbounds()) == 0 {
			return E.New("provider returned 304 without an active cached outbound set")
		}
		now := time.Now()
		if s.cacheFile != nil {
			savedSubscription := s.cacheFile.LoadSubscription(s.cacheKey)
			if savedSubscription == nil {
				return E.New("provider returned 304 without cached subscription content")
			}
			if hasInfo {
				savedSubscription.Content = updateCachedSubscriptionInfo(savedSubscription.Content, infoString)
			}
			savedSubscription.LastUpdated = now
			if err := s.cacheFile.SaveSubscription(s.cacheKey, savedSubscription); err != nil {
				return E.Cause(err, "save outbound provider cache file")
			}
		}
		s.stateAccess.Lock()
		s.subscriptionInfo = selectSubscriptionInfo(s.subscriptionInfo, info, hasInfo, true)
		s.lastUpdated = now
		s.stateAccess.Unlock()
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

	update, outboundOptions, err := s.prepareProviderFromContent(content)
	if err != nil {
		return err
	}
	now := time.Now()
	etag := response.Header.Get("Etag")
	cacheContent := []byte(content)
	if hasInfo {
		cacheContent = append([]byte(infoString+"\n"), cacheContent...)
	}
	beforePublish := func() error {
		if s.cacheFile == nil {
			return nil
		}
		if err := s.cacheFile.SaveSubscription(s.cacheKey, &adapter.SavedBinary{
			Content:     cacheContent,
			LastUpdated: now,
			LastEtag:    etag,
		}); err != nil {
			return E.Cause(err, "save outbound provider cache file")
		}
		return nil
	}
	if _, err = update.Commit(beforePublish); err != nil {
		return err
	}

	s.stateAccess.Lock()
	s.lastOutOpts = outboundOptions
	s.lastEtag = etag
	s.subscriptionInfo = selectSubscriptionInfo(s.subscriptionInfo, info, hasInfo, false)
	s.lastUpdated = now
	s.stateAccess.Unlock()
	s.logger.Info("updated outbound provider ", s.Tag())
	return nil
}

func scrubProviderURLError(err error) error {
	if urlErr, isURLError := err.(*url.Error); isURLError {
		urlErr.URL = "<provider-url>"
	}
	return err
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

func nextProviderUpdateDelay(now time.Time, lastUpdated time.Time, updateInterval time.Duration) time.Duration {
	if lastUpdated.IsZero() {
		return 0
	}
	elapsed := now.Sub(lastUpdated)
	if elapsed < 0 {
		return updateInterval
	}
	if elapsed >= updateInterval {
		return 0
	}
	return updateInterval - elapsed
}

func providerUpdateRetryDelay(updateInterval time.Duration, failureCount int) time.Duration {
	limit := providerUpdateRetryLimit
	if updateInterval > 0 && updateInterval < limit {
		limit = updateInterval
	}
	if limit <= 0 {
		limit = providerUpdateRetryBase
	}
	delay := providerUpdateRetryBase
	for attempt := 1; attempt < failureCount && delay < limit; attempt++ {
		if delay > limit/2 {
			delay = limit
			break
		}
		delay *= 2
	}
	if delay > limit {
		delay = limit
	}
	return delay
}

func (s *ProviderRemote) nextUpdateDelay() time.Duration {
	return nextProviderUpdateDelay(time.Now(), s.UpdatedAt(), s.updateInterval)
}

func (s *ProviderRemote) loopUpdate(initialDelay time.Duration) {
	timer := time.NewTimer(initialDelay)
	defer timer.Stop()
	failureCount := 0
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-timer.C:
			if delay := s.nextUpdateDelay(); delay > 0 {
				failureCount = 0
				timer.Reset(delay)
				continue
			}
			if err := s.fetch(s.ctx); err != nil {
				if s.ctx.Err() != nil {
					return
				}
				failureCount++
				s.logger.Error("update outbound provider: ", err)
				timer.Reset(providerUpdateRetryDelay(s.updateInterval, failureCount))
				continue
			}
			failureCount = 0
			timer.Reset(s.nextUpdateDelay())
		}
	}
}

func (s *ProviderRemote) prepareProviderFromContent(content string) (*adapterProvider.PreparedOutboundUpdate, []option.Outbound, error) {
	result, err := parser.ParseSubscriptionDetailed(s.ctx, content)
	if err != nil {
		return nil, nil, err
	}
	for _, skipped := range result.Skipped {
		s.logger.Warn("skip provider outbound ", skipped.Tag, " (", skipped.Type, "): ", skipped.Reason)
	}
	outboundOptions := make([]option.Outbound, 0, len(result.Outbounds))
	filtered := append([]parser.SkippedOutbound(nil), result.Skipped...)
	for _, outbound := range result.Outbounds {
		if s.exclude != nil && s.exclude.MatchString(outbound.Tag) {
			filtered = append(filtered, parser.SkippedOutbound{Tag: outbound.Tag, Type: outbound.Type, Reason: "excluded by provider filter"})
			continue
		}
		if s.include != nil && !s.include.MatchString(outbound.Tag) {
			filtered = append(filtered, parser.SkippedOutbound{Tag: outbound.Tag, Type: outbound.Type, Reason: "not matched by provider include filter"})
			continue
		}
		outboundOptions = append(outboundOptions, outbound)
	}
	if err := parser.ValidateSkippedDependencies(outboundOptions, filtered); err != nil {
		return nil, nil, err
	}
	if err := parser.ValidateProviderOutbounds(outboundOptions); err != nil {
		return nil, nil, err
	}
	s.NormalizeProviderTags(outboundOptions)
	update, err := s.PrepareUpdateOutbounds(outboundOptions)
	if err != nil {
		return nil, nil, err
	}
	return update, outboundOptions, nil
}

func (s *ProviderRemote) updateProviderFromContent(content string) error {
	update, outboundOptions, err := s.prepareProviderFromContent(content)
	if err != nil {
		return err
	}
	if _, err = update.Commit(nil); err != nil {
		return err
	}
	s.stateAccess.Lock()
	s.lastOutOpts = outboundOptions
	s.stateAccess.Unlock()
	return nil
}

func getFirstLine(content string) (string, string) {
	lines := strings.SplitN(content, "\n", 2)
	if len(lines) == 1 {
		return lines[0], ""
	}
	return lines[0], lines[1]
}

func updateCachedSubscriptionInfo(content []byte, infoString string) []byte {
	firstLine, remaining := getFirstLine(string(content))
	if _, hasInfo := parseInfo(firstLine); hasInfo {
		return []byte(infoString + "\n" + remaining)
	}
	return append([]byte(infoString+"\n"), content...)
}

func selectSubscriptionInfo(current adapter.SubscriptionInfo, next adapter.SubscriptionInfo, hasNext bool, retainMissing bool) adapter.SubscriptionInfo {
	if hasNext {
		return next
	}
	if retainMissing {
		return current
	}
	return adapter.SubscriptionInfo{}
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
