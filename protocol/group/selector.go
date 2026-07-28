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
	provider       adapter.Provider
	element        *list.Element[adapter.ProviderUpdateCallback]
	preparer       adapter.ProviderUpdatePreparer
	prepareElement *list.Element[adapter.ProviderUpdatePrepareCallback]
}

type Selector struct {
	outbound.Adapter
	ctx                          context.Context
	outbound                     adapter.OutboundManager
	provider                     adapter.ProviderManager
	connection                   adapter.ConnectionManager
	logger                       logger.ContextLogger
	staticTags                   []string
	automaticStaticTags          []string
	providerTags                 []string
	defaultTag                   string
	exclude                      *regexp.Regexp
	include                      *regexp.Regexp
	excludeType                  *regexp.Regexp
	useAllProviders              bool
	includeAllOutbounds          bool
	excludeAll                   bool
	excludeTypeAll               bool
	rebuildAccess                sync.Mutex
	access                       sync.RWMutex
	tags                         []string
	outbounds                    map[string]adapter.Outbound
	providers                    map[string]adapter.Provider
	callbacks                    []providerCallback
	selected                     atomic.TypedValue[adapter.Outbound]
	initialSelectionFinalized    bool
	interruptGroup               *interrupt.Group
	interruptExternalConnections bool
}

func NewSelector(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag string, options option.SelectorOutboundOptions) (adapter.Outbound, error) {
	includeAllOutbounds := options.IncludeAll || options.IncludeAllOutbounds
	useAllProviders := options.IncludeAll || options.UseAllProviders
	if !useAllProviders {
		if err := validateGroupProviderTags(options.Providers); err != nil {
			return nil, err
		}
	}
	var automaticStaticTags []string
	if includeAllOutbounds {
		metadata := service.FromContext[adapter.StaticOutboundMetadata](ctx)
		automaticStaticTags = append([]string(nil), metadata.Tags...)
	}
	result := &Selector{
		Adapter:                      outbound.NewAdapter(C.TypeSelector, tag, nil, options.Outbounds),
		ctx:                          ctx,
		outbound:                     service.FromContext[adapter.OutboundManager](ctx),
		provider:                     service.FromContext[adapter.ProviderManager](ctx),
		connection:                   service.FromContext[adapter.ConnectionManager](ctx),
		logger:                       logger,
		staticTags:                   append([]string(nil), options.Outbounds...),
		automaticStaticTags:          automaticStaticTags,
		providerTags:                 append([]string(nil), options.Providers...),
		defaultTag:                   options.Default,
		exclude:                      (*regexp.Regexp)(options.Exclude),
		include:                      (*regexp.Regexp)(options.Include),
		excludeType:                  (*regexp.Regexp)(options.ExcludeType),
		useAllProviders:              useAllProviders,
		includeAllOutbounds:          includeAllOutbounds,
		excludeAll:                   options.ExcludeAll,
		excludeTypeAll:               options.ExcludeTypeAll,
		outbounds:                    make(map[string]adapter.Outbound),
		providers:                    make(map[string]adapter.Provider),
		interruptGroup:               interrupt.NewGroup(),
		interruptExternalConnections: options.InterruptExistConnections,
	}
	if len(result.staticTags) == 0 && len(result.providerTags) == 0 && !result.useAllProviders && !result.includeAllOutbounds {
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
			callback := providerCallback{provider: provider}
			if preparer, loaded := provider.(adapter.ProviderUpdatePreparer); loaded {
				callback.preparer = preparer
				callback.prepareElement = preparer.RegisterPrepareCallback(s.prepareProviderUpdated)
			} else {
				callback.element = provider.RegisterCallback(s.onProviderUpdated)
			}
			s.callbacks = append(s.callbacks, callback)
		}
	} else {
		for index, tag := range s.providerTags {
			provider, loaded := s.provider.Get(tag)
			if !loaded {
				return E.New("outbound provider ", index, " not found: ", tag)
			}
			s.providers[tag] = provider
			callback := providerCallback{provider: provider}
			if preparer, loaded := provider.(adapter.ProviderUpdatePreparer); loaded {
				callback.preparer = preparer
				callback.prepareElement = preparer.RegisterPrepareCallback(s.prepareProviderUpdated)
			} else {
				callback.element = provider.RegisterCallback(s.onProviderUpdated)
			}
			s.callbacks = append(s.callbacks, callback)
		}
	}
	err := s.rebuild(nil)
	if err != nil {
		return err
	}
	if len(s.providers) == 0 {
		return s.FinalizeInitialSelection()
	}
	return nil
}

func (s *Selector) PostStart() error {
	return s.FinalizeInitialSelection()
}

func (s *Selector) Close() error {
	for _, callback := range s.callbacks {
		if callback.prepareElement != nil {
			callback.preparer.UnregisterPrepareCallback(callback.prepareElement)
		} else {
			callback.provider.UnregisterCallback(callback.element)
		}
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
	s.rebuildAccess.Lock()
	defer s.rebuildAccess.Unlock()
	s.access.RLock()
	detour, loaded := s.outbounds[tag]
	s.access.RUnlock()
	if !loaded {
		return false
	}
	s.initialSelectionFinalized = true
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
	return s.rebuild(nil)
}

type selectorState struct {
	tags        []string
	outbounds   map[string]adapter.Outbound
	selected    adapter.Outbound
	oldSelected adapter.Outbound
}

// FinalizeInitialSelection resolves the startup cache/default selection after
// all configured providers have completed their initial load.
func (s *Selector) FinalizeInitialSelection() error {
	s.rebuildAccess.Lock()
	defer s.rebuildAccess.Unlock()
	if s.initialSelectionFinalized {
		return nil
	}
	state, err := s.buildState(nil, true)
	if err != nil {
		return err
	}
	s.applyState(state)
	s.initialSelectionFinalized = true
	return nil
}

type selectorProviderUpdatePreparation struct {
	selector *Selector
	state    selectorState
	finished bool
}

func (p *selectorProviderUpdatePreparation) Commit() {
	if p == nil || p.finished {
		return
	}
	p.finished = true
	p.selector.applyState(p.state)
	p.selector.rebuildAccess.Unlock()
}

func (p *selectorProviderUpdatePreparation) Abort() {
	if p == nil || p.finished {
		return
	}
	p.finished = true
	p.selector.rebuildAccess.Unlock()
}

func (s *Selector) prepareProviderUpdated(tag string, outbounds []adapter.Outbound) (adapter.ProviderUpdatePreparation, error) {
	if _, loaded := s.providers[tag]; !loaded {
		return nil, E.New("outbound provider not found: ", tag)
	}
	s.rebuildAccess.Lock()
	state, err := s.buildState(map[string][]adapter.Outbound{tag: outbounds}, false)
	if err != nil {
		s.rebuildAccess.Unlock()
		return nil, err
	}
	s.access.RLock()
	wasAvailable := len(s.tags) > 0
	s.access.RUnlock()
	if wasAvailable && len(state.tags) == 0 {
		s.rebuildAccess.Unlock()
		return nil, E.New("provider update would leave selector[", s.Tag(), "] without an available outbound")
	}
	return &selectorProviderUpdatePreparation{selector: s, state: state}, nil
}

func (s *Selector) rebuild(providerOverrides map[string][]adapter.Outbound) error {
	s.rebuildAccess.Lock()
	defer s.rebuildAccess.Unlock()
	state, err := s.buildState(providerOverrides, false)
	if err != nil {
		return err
	}
	s.applyState(state)
	return nil
}

func (s *Selector) buildState(providerOverrides map[string][]adapter.Outbound, validateDefault bool) (selectorState, error) {
	collected, tags, err := collectGroupOutbounds(
		s.outbound,
		s.Tag(),
		s.staticTags,
		s.automaticStaticTags,
		s.providerTags,
		s.providers,
		providerOverrides,
		groupFilterOptions{
			include:        s.include,
			exclude:        s.exclude,
			excludeType:    s.excludeType,
			excludeAll:     s.excludeAll,
			excludeTypeAll: s.excludeTypeAll,
		},
	)
	if err != nil {
		return selectorState{}, err
	}
	outbounds := make(map[string]adapter.Outbound, len(collected))
	for index, tag := range tags {
		outbounds[tag] = collected[index]
	}

	oldSelected := s.selected.Load()
	selected := oldSelected
	if !s.initialSelectionFinalized {
		// The current object may only be a temporary startup fallback while
		// cache/default nodes are still waiting for their providers.
		selected = nil
	}
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
		if selected == nil && validateDefault {
			return selectorState{}, E.New("default outbound not found: ", s.defaultTag)
		}
	}
	if selected == nil && len(tags) > 0 {
		selected = outbounds[tags[0]]
	}
	return selectorState{tags: tags, outbounds: outbounds, selected: selected, oldSelected: oldSelected}, nil
}

func (s *Selector) applyState(state selectorState) {
	s.access.Lock()
	s.tags = state.tags
	s.outbounds = state.outbounds
	s.access.Unlock()
	if s.selected.Swap(state.selected) != state.selected && state.oldSelected != nil {
		s.interruptGroup.Interrupt(s.interruptExternalConnections)
	}
}
