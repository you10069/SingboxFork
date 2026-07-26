package group

import (
	"context"
	"net"
	"regexp"
	"sync"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/outbound"
	"github.com/sagernet/sing-box/common/interrupt"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/atomic"
	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/common/logger"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/common/x/list"
	"github.com/sagernet/sing/service"
)

func RegisterSelector(registry *outbound.Registry) {
	outbound.Register[option.SelectorOutboundOptions](registry, C.TypeSelector, NewSelector)
}

var (
	_ adapter.OutboundGroup             = (*Selector)(nil)
	_ adapter.ConnectionHandlerEx       = (*Selector)(nil)
	_ adapter.PacketConnectionHandlerEx = (*Selector)(nil)
)

type providerCallback struct {
	provider adapter.Provider
	element  *list.Element[adapter.ProviderUpdateCallback]
}

type Selector struct {
	outbound.Adapter
	ctx                          context.Context
	outbound                     adapter.OutboundManager
	provider                     adapter.ProviderManager
	connection                   adapter.ConnectionManager
	logger                       logger.ContextLogger
	staticTags                   []string
	providerTags                 []string
	defaultTag                   string
	exclude                      *regexp.Regexp
	include                      *regexp.Regexp
	useAllProviders              bool
	access                       sync.RWMutex
	tags                         []string
	outbounds                    map[string]adapter.Outbound
	providers                    map[string]adapter.Provider
	callbacks                    []providerCallback
	selected                     atomic.TypedValue[adapter.Outbound]
	interruptGroup               *interrupt.Group
	interruptExternalConnections bool
}

func NewSelector(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag string, options option.SelectorOutboundOptions) (adapter.Outbound, error) {
	result := &Selector{
		Adapter:                      outbound.NewAdapter(C.TypeSelector, tag, nil, options.Outbounds),
		ctx:                          ctx,
		outbound:                     service.FromContext[adapter.OutboundManager](ctx),
		provider:                     service.FromContext[adapter.ProviderManager](ctx),
		connection:                   service.FromContext[adapter.ConnectionManager](ctx),
		logger:                       logger,
		staticTags:                   append([]string(nil), options.Outbounds...),
		providerTags:                 append([]string(nil), options.Providers...),
		defaultTag:                   options.Default,
		exclude:                      (*regexp.Regexp)(options.Exclude),
		include:                      (*regexp.Regexp)(options.Include),
		useAllProviders:              options.UseAllProviders,
		outbounds:                    make(map[string]adapter.Outbound),
		providers:                    make(map[string]adapter.Provider),
		interruptGroup:               interrupt.NewGroup(),
		interruptExternalConnections: options.InterruptExistConnections,
	}
	if len(result.staticTags) == 0 && len(result.providerTags) == 0 && !result.useAllProviders {
		return nil, E.New("missing outbound and provider tags")
	}
	return result, nil
}

func (s *Selector) Network() []string {
	selected := s.selected.Load()
	if selected == nil {
		return []string{N.NetworkTCP, N.NetworkUDP}
	}
	return selected.Network()
}

func (s *Selector) Start() error {
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
	return s.rebuild("")
}

func (s *Selector) Close() error {
	for _, callback := range s.callbacks {
		callback.provider.UnregisterCallback(callback.element)
	}
	s.callbacks = nil
	return nil
}

func (s *Selector) Now() string {
	selected := s.selected.Load()
	if selected == nil {
		return ""
	}
	return selected.Tag()
}

func (s *Selector) All() []string {
	s.access.RLock()
	defer s.access.RUnlock()
	return append([]string(nil), s.tags...)
}

func (s *Selector) SelectOutbound(tag string) bool {
	s.access.RLock()
	detour, loaded := s.outbounds[tag]
	s.access.RUnlock()
	if !loaded {
		return false
	}
	if s.selected.Swap(detour) == detour {
		return true
	}
	if s.Tag() != "" {
		if cacheFile := service.FromContext[adapter.CacheFile](s.ctx); cacheFile != nil {
			if err := cacheFile.StoreSelected(s.Tag(), tag); err != nil {
				s.logger.Error("store selected: ", err)
			}
		}
	}
	s.interruptGroup.Interrupt(s.interruptExternalConnections)
	return true
}

func (s *Selector) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	selected := s.selected.Load()
	if selected == nil {
		return nil, E.New("selector has no available outbound")
	}
	conn, err := selected.DialContext(ctx, network, destination)
	if err != nil {
		return nil, err
	}
	return s.interruptGroup.NewConn(conn, interrupt.IsExternalConnectionFromContext(ctx)), nil
}

func (s *Selector) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	selected := s.selected.Load()
	if selected == nil {
		return nil, E.New("selector has no available outbound")
	}
	conn, err := selected.ListenPacket(ctx, destination)
	if err != nil {
		return nil, err
	}
	return s.interruptGroup.NewPacketConn(conn, interrupt.IsExternalConnectionFromContext(ctx)), nil
}

func (s *Selector) NewConnectionEx(ctx context.Context, conn net.Conn, metadata adapter.InboundContext, onClose N.CloseHandlerFunc) {
	ctx = interrupt.ContextWithIsExternalConnection(ctx)
	if s.selected.Load() == nil {
		N.CloseOnHandshakeFailure(conn, onClose, E.New("selector has no available outbound"))
		return
	}
	s.connection.NewConnection(ctx, s, conn, metadata, onClose)
}

func (s *Selector) NewPacketConnectionEx(ctx context.Context, conn N.PacketConn, metadata adapter.InboundContext, onClose N.CloseHandlerFunc) {
	ctx = interrupt.ContextWithIsExternalConnection(ctx)
	if s.selected.Load() == nil {
		N.CloseOnHandshakeFailure(conn, onClose, E.New("selector has no available outbound"))
		return
	}
	s.connection.NewPacketConnection(ctx, s, conn, metadata, onClose)
}

func RealTag(detour adapter.Outbound) string {
	if group, isGroup := detour.(adapter.OutboundGroup); isGroup {
		return group.Now()
	}
	return detour.Tag()
}

func (s *Selector) onProviderUpdated(tag string) error {
	if _, loaded := s.providers[tag]; !loaded {
		return E.New("outbound provider not found: ", tag)
	}
	return s.rebuild(tag)
}

func (s *Selector) rebuild(_ string) error {
	tags := make([]string, 0)
	outbounds := make(map[string]adapter.Outbound)
	for index, tag := range s.staticTags {
		detour, loaded := s.outbound.Outbound(tag)
		if !loaded {
			return E.New("outbound ", index, " not found: ", tag)
		}
		tags = append(tags, tag)
		outbounds[tag] = detour
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
			outbounds[tag] = detour
		}
	}

	oldSelected := s.selected.Load()
	selected := oldSelected
	if selected != nil {
		selected = outbounds[selected.Tag()]
	}
	if selected == nil && s.Tag() != "" {
		if cacheFile := service.FromContext[adapter.CacheFile](s.ctx); cacheFile != nil {
			selected = outbounds[cacheFile.LoadSelected(s.Tag())]
		}
	}
	if selected == nil && s.defaultTag != "" {
		selected = outbounds[s.defaultTag]
	}
	if selected == nil && len(tags) > 0 {
		selected = outbounds[tags[0]]
	}

	s.access.Lock()
	s.tags = tags
	s.outbounds = outbounds
	s.access.Unlock()
	if s.selected.Swap(selected) != selected && oldSelected != nil {
		s.interruptGroup.Interrupt(s.interruptExternalConnections)
	}
	return nil
}
