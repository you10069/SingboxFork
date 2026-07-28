package group

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"regexp"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	adapterOutbound "github.com/sagernet/sing-box/adapter/outbound"
	"github.com/sagernet/sing-box/common/interrupt"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-dns"
	L "github.com/sagernet/sing/common/logger"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/common/x/list"
	"github.com/sagernet/sing/service"
	"github.com/stretchr/testify/require"
)

type candidateTestOutbound struct {
	tag      string
	typeName string
}

func (o *candidateTestOutbound) Type() string           { return o.typeName }
func (o *candidateTestOutbound) Tag() string            { return o.tag }
func (o *candidateTestOutbound) Network() []string      { return []string{"tcp", "udp"} }
func (o *candidateTestOutbound) Dependencies() []string { return nil }
func (o *candidateTestOutbound) DialContext(context.Context, string, M.Socksaddr) (net.Conn, error) {
	return nil, errors.New("not implemented")
}
func (o *candidateTestOutbound) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	return nil, errors.New("not implemented")
}

type candidateTestManager struct {
	outbounds []adapter.Outbound
	byTag     map[string]adapter.Outbound
}

func newCandidateTestManager(outbounds ...adapter.Outbound) *candidateTestManager {
	byTag := make(map[string]adapter.Outbound, len(outbounds))
	for _, outbound := range outbounds {
		byTag[outbound.Tag()] = outbound
	}
	return &candidateTestManager{outbounds: outbounds, byTag: byTag}
}
func (*candidateTestManager) Start(adapter.StartStage) error { return nil }
func (*candidateTestManager) Close() error                   { return nil }
func (m *candidateTestManager) Outbounds() []adapter.Outbound {
	return append([]adapter.Outbound(nil), m.outbounds...)
}
func (m *candidateTestManager) Outbound(tag string) (adapter.Outbound, bool) {
	outbound, ok := m.byTag[tag]
	return outbound, ok
}
func (m *candidateTestManager) Default() adapter.Outbound {
	if len(m.outbounds) == 0 {
		return nil
	}
	return m.outbounds[0]
}
func (*candidateTestManager) Remove(string) error { return errors.New("not implemented") }
func (*candidateTestManager) Create(context.Context, adapter.Router, log.ContextLogger, string, string, any) error {
	return errors.New("not implemented")
}

type selectorTestConnectionManager struct {
	connectionDialer       N.Dialer
	packetConnectionDialer N.Dialer
	connectionExternal     bool
	packetExternal         bool
}

func (*selectorTestConnectionManager) Start(adapter.StartStage) error { return nil }
func (*selectorTestConnectionManager) Close() error                   { return nil }
func (m *selectorTestConnectionManager) NewConnection(ctx context.Context, dialer N.Dialer, _ net.Conn, _ adapter.InboundContext, _ N.CloseHandlerFunc) {
	m.connectionDialer = dialer
	m.connectionExternal = interrupt.IsExternalConnectionFromContext(ctx)
}
func (m *selectorTestConnectionManager) NewPacketConnection(ctx context.Context, dialer N.Dialer, _ N.PacketConn, _ adapter.InboundContext, _ N.CloseHandlerFunc) {
	m.packetConnectionDialer = dialer
	m.packetExternal = interrupt.IsExternalConnectionFromContext(ctx)
}

type candidateTestProvider struct {
	tag       string
	outbounds []adapter.Outbound
}

func (*candidateTestProvider) Type() string  { return "test" }
func (p *candidateTestProvider) Tag() string { return p.tag }
func (p *candidateTestProvider) Outbounds() []adapter.Outbound {
	return append([]adapter.Outbound(nil), p.outbounds...)
}
func (p *candidateTestProvider) Outbound(tag string) (adapter.Outbound, bool) {
	for _, outbound := range p.outbounds {
		if outbound.Tag() == tag {
			return outbound, true
		}
	}
	return nil, false
}
func (*candidateTestProvider) UpdatedAt() time.Time { return time.Time{} }
func (*candidateTestProvider) HealthCheck(context.Context) (map[string]uint16, error) {
	return nil, nil
}
func (*candidateTestProvider) RegisterCallback(adapter.ProviderUpdateCallback) *list.Element[adapter.ProviderUpdateCallback] {
	return nil
}
func (*candidateTestProvider) UnregisterCallback(*list.Element[adapter.ProviderUpdateCallback]) {}

type selectorTestCacheFile struct {
	selected map[string]string
}

func (*selectorTestCacheFile) Name() string                            { return "test-cache" }
func (*selectorTestCacheFile) Start(adapter.StartStage) error          { return nil }
func (*selectorTestCacheFile) Close() error                            { return nil }
func (*selectorTestCacheFile) StoreFakeIP() bool                       { return false }
func (*selectorTestCacheFile) FakeIPMetadata() *adapter.FakeIPMetadata { return nil }
func (*selectorTestCacheFile) FakeIPSaveMetadata(*adapter.FakeIPMetadata) error {
	return nil
}
func (*selectorTestCacheFile) FakeIPSaveMetadataAsync(*adapter.FakeIPMetadata) {}
func (*selectorTestCacheFile) FakeIPStore(netip.Addr, string) error            { return nil }
func (*selectorTestCacheFile) FakeIPStoreAsync(netip.Addr, string, L.Logger)   {}
func (*selectorTestCacheFile) FakeIPLoad(netip.Addr) (string, bool)            { return "", false }
func (*selectorTestCacheFile) FakeIPLoadDomain(string, bool) (netip.Addr, bool) {
	return netip.Addr{}, false
}
func (*selectorTestCacheFile) FakeIPReset() error { return nil }
func (*selectorTestCacheFile) StoreRDRC() bool    { return false }
func (*selectorTestCacheFile) LoadRDRC(string, string, uint16) bool {
	return false
}
func (*selectorTestCacheFile) SaveRDRC(string, string, uint16) error { return nil }
func (*selectorTestCacheFile) SaveRDRCAsync(string, string, uint16, L.Logger) {
}
func (*selectorTestCacheFile) LoadMode() string                   { return "" }
func (*selectorTestCacheFile) StoreMode(string) error             { return nil }
func (c *selectorTestCacheFile) LoadSelected(group string) string { return c.selected[group] }
func (c *selectorTestCacheFile) StoreSelected(group string, selected string) error {
	c.selected[group] = selected
	return nil
}
func (*selectorTestCacheFile) LoadGroupExpand(string) (bool, bool) { return false, false }
func (*selectorTestCacheFile) StoreGroupExpand(string, bool) error { return nil }
func (*selectorTestCacheFile) LoadRuleSet(string) *adapter.SavedBinary {
	return nil
}
func (*selectorTestCacheFile) SaveRuleSet(string, *adapter.SavedBinary) error {
	return nil
}
func (*selectorTestCacheFile) LoadSubscription(string) *adapter.SavedBinary {
	return nil
}
func (*selectorTestCacheFile) SaveSubscription(string, *adapter.SavedBinary) error {
	return nil
}

var (
	_ adapter.CacheFile = (*selectorTestCacheFile)(nil)
	_ dns.RDRCStore     = (*selectorTestCacheFile)(nil)
)

func TestCollectGroupOutboundsFilterScopes(t *testing.T) {
	direct := &candidateTestOutbound{tag: "direct", typeName: "direct"}
	explicitBlocked := &candidateTestOutbound{tag: "explicit-blocked", typeName: "vless"}
	autoHK := &candidateTestOutbound{tag: "auto-hk", typeName: "vless"}
	autoJP := &candidateTestOutbound{tag: "auto-jp", typeName: "trojan"}
	providerHK := &candidateTestOutbound{tag: "provider/hk", typeName: "vless"}
	providerExpire := &candidateTestOutbound{tag: "provider/expire", typeName: "vless"}
	manager := newCandidateTestManager(direct, explicitBlocked, autoHK, autoJP)
	provider := &candidateTestProvider{tag: "provider", outbounds: []adapter.Outbound{providerHK, providerExpire}}

	outbounds, tags, err := collectGroupOutbounds(
		manager,
		"selector",
		[]string{"direct", "explicit-blocked"},
		[]string{"auto-hk", "auto-jp"},
		[]string{"provider"},
		map[string]adapter.Provider{"provider": provider},
		nil,
		groupFilterOptions{
			include:     regexp.MustCompile("hk"),
			exclude:     regexp.MustCompile("blocked|expire"),
			excludeType: regexp.MustCompile("trojan"),
		},
	)
	require.NoError(t, err)
	require.Equal(t, []string{"direct", "explicit-blocked", "auto-hk", "provider/hk"}, tags)
	require.Len(t, outbounds, 4)
}

func TestCollectGroupOutboundsExcludeAll(t *testing.T) {
	explicit := &candidateTestOutbound{tag: "explicit-blocked", typeName: "vless"}
	explicitTrojan := &candidateTestOutbound{tag: "manual-trojan", typeName: "trojan"}
	manager := newCandidateTestManager(explicit, explicitTrojan)

	_, tags, err := collectGroupOutbounds(
		manager,
		"selector",
		[]string{"explicit-blocked", "manual-trojan"},
		nil,
		nil,
		nil,
		nil,
		groupFilterOptions{
			exclude:        regexp.MustCompile("blocked"),
			excludeType:    regexp.MustCompile("trojan"),
			excludeAll:     true,
			excludeTypeAll: true,
		},
	)
	require.NoError(t, err)
	require.Empty(t, tags)
}

func TestCollectGroupOutboundsExplicitWinsDeduplication(t *testing.T) {
	node := &candidateTestOutbound{tag: "node", typeName: "vless"}
	manager := newCandidateTestManager(node)
	_, tags, err := collectGroupOutbounds(
		manager,
		"selector",
		[]string{"node"},
		[]string{"node"},
		nil,
		nil,
		nil,
		groupFilterOptions{include: regexp.MustCompile("does-not-match")},
	)
	require.NoError(t, err)
	require.Equal(t, []string{"node"}, tags)
}

func TestCollectGroupOutboundsUsesProviderOverride(t *testing.T) {
	oldNode := &candidateTestOutbound{tag: "provider/old", typeName: "vless"}
	newNode := &candidateTestOutbound{tag: "provider/new", typeName: "vless"}
	provider := &candidateTestProvider{tag: "provider", outbounds: []adapter.Outbound{oldNode}}
	_, tags, err := collectGroupOutbounds(
		newCandidateTestManager(),
		"selector",
		nil,
		nil,
		[]string{"provider"},
		map[string]adapter.Provider{"provider": provider},
		map[string][]adapter.Outbound{"provider": {newNode}},
		groupFilterOptions{},
	)
	require.NoError(t, err)
	require.Equal(t, []string{"provider/new"}, tags)
}

func TestSelectorProviderPreparationRebindsObjectByTag(t *testing.T) {
	oldNode := &candidateTestOutbound{tag: "provider/node", typeName: "vless"}
	newNode := &candidateTestOutbound{tag: "provider/node", typeName: "vless"}
	provider := &candidateTestProvider{tag: "provider", outbounds: []adapter.Outbound{oldNode}}
	selector := &Selector{
		ctx:            context.Background(),
		outbound:       newCandidateTestManager(),
		providerTags:   []string{"provider"},
		providers:      map[string]adapter.Provider{"provider": provider},
		tags:           []string{"provider/node"},
		outbounds:      map[string]adapter.Outbound{"provider/node": oldNode},
		interruptGroup: interrupt.NewGroup(),
	}
	selector.selected.Swap(oldNode)

	preparation, err := selector.prepareProviderUpdated("provider", []adapter.Outbound{newNode})
	require.NoError(t, err)
	require.Same(t, oldNode, selector.selected.Load())
	preparation.Commit()
	require.Same(t, newNode, selector.selected.Load())
	require.Equal(t, []string{"provider/node"}, selector.All())
	require.False(t, selector.initialSelectionFinalized)
}

func TestSelectorProviderPreparationRejectsRuntimeEmptyGroup(t *testing.T) {
	oldNode := &candidateTestOutbound{tag: "provider/node", typeName: "vless"}
	provider := &candidateTestProvider{tag: "provider", outbounds: []adapter.Outbound{oldNode}}
	selector := &Selector{
		ctx:            context.Background(),
		outbound:       newCandidateTestManager(),
		providerTags:   []string{"provider"},
		providers:      map[string]adapter.Provider{"provider": provider},
		tags:           []string{"provider/node"},
		outbounds:      map[string]adapter.Outbound{"provider/node": oldNode},
		interruptGroup: interrupt.NewGroup(),
	}
	selector.selected.Swap(oldNode)

	preparation, err := selector.prepareProviderUpdated("provider", nil)
	require.Error(t, err)
	require.Nil(t, preparation)
	require.Same(t, oldNode, selector.selected.Load())
}

func TestSelectorConnectionHandlersKeepInterruptWrapper(t *testing.T) {
	node := &candidateTestOutbound{tag: "node", typeName: "vless"}
	connectionManager := new(selectorTestConnectionManager)
	selector := &Selector{
		connection:     connectionManager,
		interruptGroup: interrupt.NewGroup(),
	}
	selector.selected.Swap(node)

	selector.NewConnectionEx(context.Background(), nil, adapter.InboundContext{}, nil)
	selector.NewPacketConnectionEx(context.Background(), nil, adapter.InboundContext{}, nil)

	require.Same(t, selector, connectionManager.connectionDialer)
	require.Same(t, selector, connectionManager.packetConnectionDialer)
	require.True(t, connectionManager.connectionExternal)
	require.True(t, connectionManager.packetExternal)
}

func TestSelectorStartRejectsMissingStaticDefault(t *testing.T) {
	direct := &candidateTestOutbound{tag: "direct", typeName: "direct"}
	selector := &Selector{
		Adapter:        adapterOutbound.NewAdapter(C.TypeSelector, "selector", nil, nil),
		ctx:            context.Background(),
		outbound:       newCandidateTestManager(direct),
		staticTags:     []string{"direct"},
		defaultTag:     "missing",
		outbounds:      make(map[string]adapter.Outbound),
		providers:      make(map[string]adapter.Provider),
		interruptGroup: interrupt.NewGroup(),
	}

	err := selector.Start()
	require.EqualError(t, err, "default outbound not found: missing")
	require.Equal(t, "direct", selector.Now())
	require.False(t, selector.initialSelectionFinalized)
}

func TestSelectorProviderDefaultReplacesStartupFallback(t *testing.T) {
	direct := &candidateTestOutbound{tag: "direct", typeName: "direct"}
	defaultNode := &candidateTestOutbound{tag: "provider_default", typeName: "vless"}
	provider := &candidateTestProvider{tag: "provider"}
	selector := &Selector{
		Adapter:        adapterOutbound.NewAdapter(C.TypeSelector, "selector", nil, nil),
		ctx:            context.Background(),
		outbound:       newCandidateTestManager(direct),
		staticTags:     []string{"direct"},
		providerTags:   []string{"provider"},
		defaultTag:     "provider_default",
		outbounds:      make(map[string]adapter.Outbound),
		providers:      map[string]adapter.Provider{"provider": provider},
		interruptGroup: interrupt.NewGroup(),
	}

	require.NoError(t, selector.rebuild(nil))
	require.Equal(t, "direct", selector.Now())
	require.False(t, selector.initialSelectionFinalized)

	preparation, err := selector.prepareProviderUpdated("provider", []adapter.Outbound{defaultNode})
	require.NoError(t, err)
	preparation.Commit()
	provider.outbounds = []adapter.Outbound{defaultNode}
	require.Equal(t, "provider_default", selector.Now())

	require.NoError(t, selector.FinalizeInitialSelection())
	require.True(t, selector.initialSelectionFinalized)
	require.Equal(t, "provider_default", selector.Now())
}

func TestSelectorCachedProviderSelectionIsOnlyPendingDuringStartup(t *testing.T) {
	direct := &candidateTestOutbound{tag: "direct", typeName: "direct"}
	defaultNode := &candidateTestOutbound{tag: "proxy-a", typeName: "vless"}
	cachedNode := &candidateTestOutbound{tag: "provider_proxy-b", typeName: "vless"}
	recreatedCachedNode := &candidateTestOutbound{tag: "provider_proxy-b", typeName: "vless"}
	provider := &candidateTestProvider{tag: "provider"}
	cacheFile := &selectorTestCacheFile{selected: map[string]string{"selector": "provider_proxy-b"}}
	ctx := service.ContextWith[adapter.CacheFile](context.Background(), cacheFile)
	selector := &Selector{
		Adapter:        adapterOutbound.NewAdapter(C.TypeSelector, "selector", nil, nil),
		ctx:            ctx,
		outbound:       newCandidateTestManager(direct, defaultNode),
		staticTags:     []string{"direct", "proxy-a"},
		providerTags:   []string{"provider"},
		defaultTag:     "proxy-a",
		outbounds:      make(map[string]adapter.Outbound),
		providers:      map[string]adapter.Provider{"provider": provider},
		interruptGroup: interrupt.NewGroup(),
	}

	require.NoError(t, selector.rebuild(nil))
	require.Equal(t, "proxy-a", selector.Now())

	preparation, err := selector.prepareProviderUpdated("provider", []adapter.Outbound{cachedNode})
	require.NoError(t, err)
	preparation.Commit()
	provider.outbounds = []adapter.Outbound{cachedNode}
	require.Equal(t, "provider_proxy-b", selector.Now())

	require.NoError(t, selector.FinalizeInitialSelection())
	require.True(t, selector.initialSelectionFinalized)

	preparation, err = selector.prepareProviderUpdated("provider", nil)
	require.NoError(t, err)
	preparation.Commit()
	provider.outbounds = nil
	require.Equal(t, "proxy-a", selector.Now())

	preparation, err = selector.prepareProviderUpdated("provider", []adapter.Outbound{recreatedCachedNode})
	require.NoError(t, err)
	preparation.Commit()
	provider.outbounds = []adapter.Outbound{recreatedCachedNode}
	require.Equal(t, "proxy-a", selector.Now())
}

func TestSelectorMissingCachedSelectionFallsBackToDefaultAtStartup(t *testing.T) {
	direct := &candidateTestOutbound{tag: "direct", typeName: "direct"}
	defaultNode := &candidateTestOutbound{tag: "proxy-a", typeName: "vless"}
	cachedNode := &candidateTestOutbound{tag: "provider_proxy-b", typeName: "vless"}
	provider := &candidateTestProvider{tag: "provider"}
	cacheFile := &selectorTestCacheFile{selected: map[string]string{"selector": "provider_proxy-b"}}
	ctx := service.ContextWith[adapter.CacheFile](context.Background(), cacheFile)
	selector := &Selector{
		Adapter:        adapterOutbound.NewAdapter(C.TypeSelector, "selector", nil, nil),
		ctx:            ctx,
		outbound:       newCandidateTestManager(direct, defaultNode),
		staticTags:     []string{"direct", "proxy-a"},
		providerTags:   []string{"provider"},
		defaultTag:     "proxy-a",
		outbounds:      make(map[string]adapter.Outbound),
		providers:      map[string]adapter.Provider{"provider": provider},
		interruptGroup: interrupt.NewGroup(),
	}

	require.NoError(t, selector.rebuild(nil))
	require.Equal(t, "proxy-a", selector.Now())
	require.NoError(t, selector.FinalizeInitialSelection())
	require.True(t, selector.initialSelectionFinalized)

	preparation, err := selector.prepareProviderUpdated("provider", []adapter.Outbound{cachedNode})
	require.NoError(t, err)
	preparation.Commit()
	provider.outbounds = []adapter.Outbound{cachedNode}
	require.Equal(t, "proxy-a", selector.Now())
}

func TestSelectorManualStartupSelectionCancelsPendingDefault(t *testing.T) {
	direct := &candidateTestOutbound{tag: "direct", typeName: "direct"}
	defaultNode := &candidateTestOutbound{tag: "provider_default", typeName: "vless"}
	provider := &candidateTestProvider{tag: "provider"}
	selector := &Selector{
		Adapter:        adapterOutbound.NewAdapter(C.TypeSelector, "selector", nil, nil),
		ctx:            context.Background(),
		outbound:       newCandidateTestManager(direct),
		staticTags:     []string{"direct"},
		providerTags:   []string{"provider"},
		defaultTag:     "provider_default",
		outbounds:      make(map[string]adapter.Outbound),
		providers:      map[string]adapter.Provider{"provider": provider},
		interruptGroup: interrupt.NewGroup(),
	}

	require.NoError(t, selector.rebuild(nil))
	require.Equal(t, "direct", selector.Now())
	require.True(t, selector.SelectOutbound("direct"))
	require.True(t, selector.initialSelectionFinalized)

	preparation, err := selector.prepareProviderUpdated("provider", []adapter.Outbound{defaultNode})
	require.NoError(t, err)
	preparation.Commit()
	provider.outbounds = []adapter.Outbound{defaultNode}
	require.Equal(t, "direct", selector.Now())
	require.NoError(t, selector.FinalizeInitialSelection())
	require.Equal(t, "direct", selector.Now())
}

func TestSelectorAbortedProviderPreparationKeepsStartupSelectionPending(t *testing.T) {
	direct := &candidateTestOutbound{tag: "direct", typeName: "direct"}
	defaultNode := &candidateTestOutbound{tag: "provider_default", typeName: "vless"}
	provider := &candidateTestProvider{tag: "provider"}
	selector := &Selector{
		Adapter:        adapterOutbound.NewAdapter(C.TypeSelector, "selector", nil, nil),
		ctx:            context.Background(),
		outbound:       newCandidateTestManager(direct),
		staticTags:     []string{"direct"},
		providerTags:   []string{"provider"},
		defaultTag:     "provider_default",
		outbounds:      make(map[string]adapter.Outbound),
		providers:      map[string]adapter.Provider{"provider": provider},
		interruptGroup: interrupt.NewGroup(),
	}

	require.NoError(t, selector.rebuild(nil))
	preparation, err := selector.prepareProviderUpdated("provider", []adapter.Outbound{defaultNode})
	require.NoError(t, err)
	preparation.Abort()

	require.Equal(t, "direct", selector.Now())
	require.False(t, selector.initialSelectionFinalized)
}
