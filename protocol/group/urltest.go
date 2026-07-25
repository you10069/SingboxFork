package group

import (
	"context"
	"net"
	"regexp"
	"sync"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/outbound"
	"github.com/sagernet/sing-box/common/interrupt"
	"github.com/sagernet/sing-box/common/urltest"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common"
	"github.com/sagernet/sing/common/atomic"
	"github.com/sagernet/sing/common/batch"
	E "github.com/sagernet/sing/common/exceptions"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/common/x/list"
	"github.com/sagernet/sing/service"
	"github.com/sagernet/sing/service/pause"
)

func RegisterURLTest(registry *outbound.Registry) {
	outbound.Register[option.URLTestOutboundOptions](registry, C.TypeURLTest, NewURLTest)
}

var _ adapter.OutboundGroup = (*URLTest)(nil)

type URLTest struct {
	outbound.Adapter
	ctx                          context.Context
	router                       adapter.Router
	outbound                     adapter.OutboundManager
	provider                     adapter.ProviderManager
	connection                   adapter.ConnectionManager
	logger                       log.ContextLogger
	staticTags                   []string
	providerTags                 []string
	exclude                      *regexp.Regexp
	include                      *regexp.Regexp
	useAllProviders              bool
	providers                    map[string]adapter.Provider
	callbacks                    []providerCallback
	access                       sync.RWMutex
	tags                         []string
	link                         string
	interval                     time.Duration
	tolerance                    uint16
	idleTimeout                  time.Duration
	group                        *URLTestGroup
	interruptExternalConnections bool
}

func NewURLTest(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag string, options option.URLTestOutboundOptions) (adapter.Outbound, error) {
	result := &URLTest{
		Adapter:                      outbound.NewAdapter(C.TypeURLTest, tag, []string{N.NetworkTCP, N.NetworkUDP}, options.Outbounds),
		ctx:                          ctx,
		router:                       router,
		outbound:                     service.FromContext[adapter.OutboundManager](ctx),
		provider:                     service.FromContext[adapter.ProviderManager](ctx),
		connection:                   service.FromContext[adapter.ConnectionManager](ctx),
		logger:                       logger,
		staticTags:                   append([]string(nil), options.Outbounds...),
		providerTags:                 append([]string(nil), options.Providers...),
		exclude:                      (*regexp.Regexp)(options.Exclude),
		include:                      (*regexp.Regexp)(options.Include),
		useAllProviders:              options.UseAllProviders,
		providers:                    make(map[string]adapter.Provider),
		link:                         options.URL,
		interval:                     time.Duration(options.Interval),
		tolerance:                    options.Tolerance,
		idleTimeout:                  time.Duration(options.IdleTimeout),
		interruptExternalConnections: options.InterruptExistConnections,
	}
	if len(result.staticTags) == 0 && len(result.providerTags) == 0 && !result.useAllProviders {
		return nil, E.New("missing outbound and provider tags")
	}
	return result, nil
}

func (s *URLTest) Start() error {
	if s.provider == nil && (len(s.providerTags) > 0 || s.useAllProviders) {
		return E.New("missing provider manager")
	}
	if s.useAllProviders {
		s.providerTags = nil
		for _, provider := range s.provider.Providers() {
			s.providerTags = append(s.providerTags, provider.Tag())
			s.providers[provider.Tag()] = provider
			s.callbacks = append(s.callbacks, providerCallback{provider, provider.RegisterCallback(s.onProviderUpdated)})
		}
	} else {
		for index, tag := range s.providerTags {
			provider, loaded := s.provider.Get(tag)
			if !loaded {
				return E.New("outbound provider ", index, " not found: ", tag)
			}
			s.providers[tag] = provider
			s.callbacks = append(s.callbacks, providerCallback{provider, provider.RegisterCallback(s.onProviderUpdated)})
		}
	}
	initialOutbounds, tags, err := s.collectOutbounds()
	if err != nil {
		return err
	}
	group, err := NewURLTestGroup(s.ctx, s.outbound, s.logger, initialOutbounds, s.link, s.interval, s.tolerance, s.idleTimeout, s.interruptExternalConnections)
	if err != nil {
		return err
	}
	s.access.Lock()
	s.tags = tags
	s.group = group
	s.access.Unlock()
	return nil
}

func (s *URLTest) PostStart() error {
	s.access.RLock()
	group := s.group
	s.access.RUnlock()
	if group != nil {
		group.PostStart()
	}
	return nil
}

func (s *URLTest) Close() error {
	for _, callback := range s.callbacks {
		callback.provider.UnregisterCallback(callback.element)
	}
	s.callbacks = nil
	s.access.RLock()
	group := s.group
	s.access.RUnlock()
	return common.Close(common.PtrOrNil(group))
}

func (s *URLTest) Now() string {
	s.access.RLock()
	group := s.group
	s.access.RUnlock()
	if group == nil {
		return ""
	}
	return group.Now()
}

func (s *URLTest) All() []string {
	s.access.RLock()
	defer s.access.RUnlock()
	return append([]string(nil), s.tags...)
}

func (s *URLTest) URLTest(ctx context.Context) (map[string]uint16, error) {
	s.access.RLock()
	group := s.group
	s.access.RUnlock()
	if group == nil {
		return nil, E.New("URLTest group is not started")
	}
	return group.URLTest(ctx)
}

func (s *URLTest) CheckOutbounds() {
	s.access.RLock()
	group := s.group
	s.access.RUnlock()
	if group != nil {
		group.CheckOutbounds(true)
	}
}

func (s *URLTest) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	s.access.RLock()
	group := s.group
	s.access.RUnlock()
	if group == nil {
		return nil, E.New("URLTest group is not started")
	}
	group.Touch()
	selected := group.Selected(network)
	if selected == nil {
		selected, _ = group.Select(network)
	}
	if selected == nil {
		return nil, E.New("missing supported outbound")
	}
	conn, err := selected.DialContext(ctx, network, destination)
	if err == nil {
		return group.interruptGroup.NewConn(conn, interrupt.IsExternalConnectionFromContext(ctx)), nil
	}
	s.logger.ErrorContext(ctx, err)
	group.history.DeleteURLTestHistory(RealTag(selected))
	return nil, err
}

func (s *URLTest) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	s.access.RLock()
	group := s.group
	s.access.RUnlock()
	if group == nil {
		return nil, E.New("URLTest group is not started")
	}
	group.Touch()
	selected := group.Selected(N.NetworkUDP)
	if selected == nil {
		selected, _ = group.Select(N.NetworkUDP)
	}
	if selected == nil {
		return nil, E.New("missing supported outbound")
	}
	conn, err := selected.ListenPacket(ctx, destination)
	if err == nil {
		return group.interruptGroup.NewPacketConn(conn, interrupt.IsExternalConnectionFromContext(ctx)), nil
	}
	s.logger.ErrorContext(ctx, err)
	group.history.DeleteURLTestHistory(RealTag(selected))
	return nil, err
}

func (s *URLTest) NewConnectionEx(ctx context.Context, conn net.Conn, metadata adapter.InboundContext, onClose N.CloseHandlerFunc) {
	ctx = interrupt.ContextWithIsExternalConnection(ctx)
	s.connection.NewConnection(ctx, s, conn, metadata, onClose)
}

func (s *URLTest) NewPacketConnectionEx(ctx context.Context, conn N.PacketConn, metadata adapter.InboundContext, onClose N.CloseHandlerFunc) {
	ctx = interrupt.ContextWithIsExternalConnection(ctx)
	s.connection.NewPacketConnection(ctx, s, conn, metadata, onClose)
}

func (s *URLTest) onProviderUpdated(tag string) error {
	if _, loaded := s.providers[tag]; !loaded {
		return E.New("outbound provider not found: ", tag)
	}
	outbounds, tags, err := s.collectOutbounds()
	if err != nil {
		return err
	}
	s.access.Lock()
	s.tags = tags
	group := s.group
	s.access.Unlock()
	if group != nil {
		group.UpdateOutbounds(outbounds)
	}
	return nil
}

func (s *URLTest) collectOutbounds() ([]adapter.Outbound, []string, error) {
	outbounds := make([]adapter.Outbound, 0)
	tags := make([]string, 0)
	for index, tag := range s.staticTags {
		detour, loaded := s.outbound.Outbound(tag)
		if !loaded {
			return nil, nil, E.New("outbound ", index, " not found: ", tag)
		}
		tags = append(tags, tag)
		outbounds = append(outbounds, detour)
	}
	for _, providerTag := range s.providerTags {
		provider := s.providers[providerTag]
		if provider == nil {
			continue
		}
		for _, detour := range provider.Outbounds() {
			tag := detour.Tag()
			if s.exclude != nil && s.exclude.MatchString(tag) {
				continue
			}
			if s.include != nil && !s.include.MatchString(tag) {
				continue
			}
			tags = append(tags, tag)
			outbounds = append(outbounds, detour)
		}
	}
	return outbounds, tags, nil
}

type URLTestGroup struct {
	ctx                          context.Context
	outbound                     adapter.OutboundManager
	pause                        pause.Manager
	pauseCallback                *list.Element[pause.Callback]
	logger                       log.Logger
	outbounds                    []adapter.Outbound
	link                         string
	interval                     time.Duration
	tolerance                    uint16
	idleTimeout                  time.Duration
	history                      *urltest.HistoryStorage
	checking                     atomic.Bool
	selectedOutboundTCP          adapter.Outbound
	selectedOutboundUDP          adapter.Outbound
	interruptGroup               *interrupt.Group
	interruptExternalConnections bool
	access                       sync.RWMutex
	ticker                       *time.Ticker
	close                        chan struct{}
	closeOnce                    sync.Once
	started                      bool
	lastActive                   atomic.TypedValue[time.Time]
}

func NewURLTestGroup(ctx context.Context, outboundManager adapter.OutboundManager, logger log.Logger, outbounds []adapter.Outbound, link string, interval time.Duration, tolerance uint16, idleTimeout time.Duration, interruptExternalConnections bool) (*URLTestGroup, error) {
	if interval == 0 {
		interval = C.DefaultURLTestInterval
	}
	if tolerance == 0 {
		tolerance = 50
	}
	if idleTimeout == 0 {
		idleTimeout = C.DefaultURLTestIdleTimeout
	}
	if interval > idleTimeout {
		return nil, E.New("interval must be less or equal than idle_timeout")
	}
	var history *urltest.HistoryStorage
	if history = service.PtrFromContext[urltest.HistoryStorage](ctx); history != nil {
	} else if clashServer := service.FromContext[adapter.ClashServer](ctx); clashServer != nil {
		history = clashServer.HistoryStorage()
	} else {
		history = urltest.NewHistoryStorage()
	}
	return &URLTestGroup{
		ctx:                          ctx,
		outbound:                     outboundManager,
		logger:                       logger,
		outbounds:                    append([]adapter.Outbound(nil), outbounds...),
		link:                         link,
		interval:                     interval,
		tolerance:                    tolerance,
		idleTimeout:                  idleTimeout,
		history:                      history,
		close:                        make(chan struct{}),
		pause:                        service.FromContext[pause.Manager](ctx),
		interruptGroup:               interrupt.NewGroup(),
		interruptExternalConnections: interruptExternalConnections,
	}, nil
}

func (g *URLTestGroup) PostStart() {
	g.access.Lock()
	g.started = true
	g.lastActive.Store(time.Now())
	g.access.Unlock()
	go g.CheckOutbounds(false)
}

func (g *URLTestGroup) Now() string {
	g.access.RLock()
	defer g.access.RUnlock()
	if g.selectedOutboundTCP != nil {
		return g.selectedOutboundTCP.Tag()
	}
	if g.selectedOutboundUDP != nil {
		return g.selectedOutboundUDP.Tag()
	}
	return ""
}

func (g *URLTestGroup) Selected(network string) adapter.Outbound {
	g.access.RLock()
	defer g.access.RUnlock()
	if N.NetworkName(network) == N.NetworkUDP {
		return g.selectedOutboundUDP
	}
	return g.selectedOutboundTCP
}

func (g *URLTestGroup) UpdateOutbounds(outbounds []adapter.Outbound) {
	g.access.Lock()
	g.outbounds = append([]adapter.Outbound(nil), outbounds...)
	if !containsOutbound(g.outbounds, g.selectedOutboundTCP) {
		g.selectedOutboundTCP = nil
	}
	if !containsOutbound(g.outbounds, g.selectedOutboundUDP) {
		g.selectedOutboundUDP = nil
	}
	started := g.started
	if g.ticker != nil {
		g.ticker.Reset(g.interval)
	}
	g.access.Unlock()
	g.performUpdateCheck()
	if started {
		go g.CheckOutbounds(true)
	}
}

func containsOutbound(outbounds []adapter.Outbound, target adapter.Outbound) bool {
	if target == nil {
		return false
	}
	return common.Contains(outbounds, target)
}

func (g *URLTestGroup) Touch() {
	g.access.Lock()
	defer g.access.Unlock()
	if !g.started {
		return
	}
	if g.ticker != nil {
		g.lastActive.Store(time.Now())
		return
	}
	g.ticker = time.NewTicker(g.interval)
	go g.loopCheck(g.ticker)
	g.pauseCallback = pause.RegisterTicker(g.pause, g.ticker, g.interval, nil)
}

func (g *URLTestGroup) Close() error {
	g.access.Lock()
	if g.ticker != nil {
		g.ticker.Stop()
		g.ticker = nil
	}
	if g.pauseCallback != nil {
		g.pause.UnregisterCallback(g.pauseCallback)
		g.pauseCallback = nil
	}
	g.access.Unlock()
	g.closeOnce.Do(func() { close(g.close) })
	return nil
}

func (g *URLTestGroup) Select(network string) (adapter.Outbound, bool) {
	g.access.RLock()
	outbounds := append([]adapter.Outbound(nil), g.outbounds...)
	var current adapter.Outbound
	if network == N.NetworkUDP {
		current = g.selectedOutboundUDP
	} else {
		current = g.selectedOutboundTCP
	}
	g.access.RUnlock()

	var minDelay uint16
	var minOutbound adapter.Outbound
	if current != nil {
		if history := g.history.LoadURLTestHistory(RealTag(current)); history != nil {
			minOutbound = current
			minDelay = history.Delay
		}
	}
	for _, detour := range outbounds {
		if !common.Contains(detour.Network(), network) {
			continue
		}
		history := g.history.LoadURLTestHistory(RealTag(detour))
		if history == nil {
			continue
		}
		if minDelay == 0 || minDelay > history.Delay+g.tolerance {
			minDelay = history.Delay
			minOutbound = detour
		}
	}
	if minOutbound == nil {
		for _, detour := range outbounds {
			if common.Contains(detour.Network(), network) {
				return detour, false
			}
		}
		return nil, false
	}
	return minOutbound, true
}

func (g *URLTestGroup) loopCheck(ticker *time.Ticker) {
	if time.Since(g.lastActive.Load()) > g.interval {
		g.lastActive.Store(time.Now())
		g.CheckOutbounds(false)
	}
	for {
		select {
		case <-g.close:
			return
		case <-ticker.C:
		}
		if time.Since(g.lastActive.Load()) > g.idleTimeout {
			g.access.Lock()
			if g.ticker == ticker {
				g.ticker.Stop()
				g.ticker = nil
				if g.pauseCallback != nil {
					g.pause.UnregisterCallback(g.pauseCallback)
					g.pauseCallback = nil
				}
			}
			g.access.Unlock()
			return
		}
		g.CheckOutbounds(false)
	}
}

func (g *URLTestGroup) CheckOutbounds(force bool) {
	_, _ = g.urlTest(g.ctx, force)
}

func (g *URLTestGroup) URLTest(ctx context.Context) (map[string]uint16, error) {
	return g.urlTest(ctx, false)
}

func (g *URLTestGroup) urlTest(ctx context.Context, force bool) (map[string]uint16, error) {
	result := make(map[string]uint16)
	if g.checking.Swap(true) {
		return result, nil
	}
	defer g.checking.Store(false)
	g.access.RLock()
	outbounds := append([]adapter.Outbound(nil), g.outbounds...)
	g.access.RUnlock()
	batchGroup, _ := batch.New(ctx, batch.WithConcurrencyNum[any](10))
	checked := make(map[string]bool)
	var resultAccess sync.Mutex
	for _, detour := range outbounds {
		detour := detour
		tag := detour.Tag()
		realTag := RealTag(detour)
		if realTag == "" || checked[realTag] {
			continue
		}
		history := g.history.LoadURLTestHistory(realTag)
		if !force && history != nil && time.Since(history.Time) < g.interval {
			continue
		}
		checked[realTag] = true
		testOutbound, loaded := g.outbound.Outbound(realTag)
		if !loaded {
			continue
		}
		batchGroup.Go(realTag, func() (any, error) {
			testContext, cancel := context.WithTimeout(ctx, C.TCPTimeout)
			defer cancel()
			delay, err := urltest.URLTest(testContext, g.link, testOutbound)
			if err != nil {
				g.logger.Debug("outbound ", tag, " unavailable: ", err)
				g.history.DeleteURLTestHistory(realTag)
			} else {
				g.logger.Debug("outbound ", tag, " available: ", delay, "ms")
				g.history.StoreURLTestHistory(realTag, &urltest.History{Time: time.Now(), Delay: delay})
				resultAccess.Lock()
				result[tag] = delay
				resultAccess.Unlock()
			}
			return nil, nil
		})
	}
	batchGroup.Wait()
	select {
	case <-ctx.Done():
	default:
		g.performUpdateCheck()
	}
	return result, nil
}

func (g *URLTestGroup) performUpdateCheck() {
	tcpOutbound, tcpExists := g.Select(N.NetworkTCP)
	udpOutbound, udpExists := g.Select(N.NetworkUDP)
	g.access.Lock()
	updated := false
	if tcpOutbound != nil && (g.selectedOutboundTCP == nil || (tcpExists && tcpOutbound != g.selectedOutboundTCP)) {
		updated = g.selectedOutboundTCP != nil
		g.selectedOutboundTCP = tcpOutbound
	} else if tcpOutbound == nil {
		g.selectedOutboundTCP = nil
	}
	if udpOutbound != nil && (g.selectedOutboundUDP == nil || (udpExists && udpOutbound != g.selectedOutboundUDP)) {
		updated = updated || g.selectedOutboundUDP != nil
		g.selectedOutboundUDP = udpOutbound
	} else if udpOutbound == nil {
		g.selectedOutboundUDP = nil
	}
	g.access.Unlock()
	if updated {
		g.interruptGroup.Interrupt(g.interruptExternalConnections)
	}
}
