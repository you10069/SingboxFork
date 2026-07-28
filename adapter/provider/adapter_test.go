package provider

import (
	"bufio"
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/urltest"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	providerParser "github.com/sagernet/sing-box/provider/parser"
	M "github.com/sagernet/sing/common/metadata"
	"github.com/stretchr/testify/require"
)

type providerHealthTestOutbound struct {
	tag                string
	started            chan struct{}
	startOnce          sync.Once
	release            <-chan struct{}
	ignoreCancellation bool
}

func (o *providerHealthTestOutbound) Type() string           { return "test" }
func (o *providerHealthTestOutbound) Tag() string            { return o.tag }
func (o *providerHealthTestOutbound) Network() []string      { return []string{"tcp"} }
func (o *providerHealthTestOutbound) Dependencies() []string { return nil }
func (o *providerHealthTestOutbound) DialContext(ctx context.Context, _ string, _ M.Socksaddr) (net.Conn, error) {
	o.startOnce.Do(func() { close(o.started) })
	if o.release == nil {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if o.ignoreCancellation {
		<-o.release
	} else {
		select {
		case <-o.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	client, server := net.Pipe()
	go func() {
		defer server.Close()
		reader := bufio.NewReader(server)
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			if line == "\r\n" {
				break
			}
		}
		_, _ = server.Write([]byte("HTTP/1.1 204 No Content\r\nConnection: close\r\n\r\n"))
	}()
	return client, nil
}
func (*providerHealthTestOutbound) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	return nil, errors.New("not implemented")
}

type providerHealthTestManager struct {
	access    sync.RWMutex
	outbounds []adapter.Outbound
	byTag     map[string]adapter.Outbound
}

func newProviderHealthTestManager(outbounds ...adapter.Outbound) *providerHealthTestManager {
	manager := &providerHealthTestManager{}
	manager.set(outbounds...)
	return manager
}

func (m *providerHealthTestManager) set(outbounds ...adapter.Outbound) {
	m.access.Lock()
	defer m.access.Unlock()
	m.outbounds = append([]adapter.Outbound(nil), outbounds...)
	m.byTag = make(map[string]adapter.Outbound, len(outbounds))
	for _, outbound := range outbounds {
		m.byTag[outbound.Tag()] = outbound
	}
}

func (*providerHealthTestManager) Start(adapter.StartStage) error { return nil }
func (*providerHealthTestManager) Close() error                   { return nil }
func (m *providerHealthTestManager) Outbounds() []adapter.Outbound {
	m.access.RLock()
	defer m.access.RUnlock()
	return append([]adapter.Outbound(nil), m.outbounds...)
}
func (m *providerHealthTestManager) Outbound(tag string) (adapter.Outbound, bool) {
	m.access.RLock()
	defer m.access.RUnlock()
	outbound, loaded := m.byTag[tag]
	return outbound, loaded
}
func (m *providerHealthTestManager) Default() adapter.Outbound {
	m.access.RLock()
	defer m.access.RUnlock()
	if len(m.outbounds) == 0 {
		return nil
	}
	return m.outbounds[0]
}
func (m *providerHealthTestManager) Remove(tag string) error {
	m.access.Lock()
	defer m.access.Unlock()
	outbound, loaded := m.byTag[tag]
	if !loaded {
		return errors.New("outbound not found")
	}
	delete(m.byTag, tag)
	for index, candidate := range m.outbounds {
		if candidate == outbound {
			m.outbounds = append(m.outbounds[:index], m.outbounds[index+1:]...)
			break
		}
	}
	return nil
}
func (*providerHealthTestManager) Create(context.Context, adapter.Router, log.ContextLogger, string, string, any) error {
	return errors.New("not implemented")
}

func TestCloneProviderOutboundsKeepsCanonicalDetour(t *testing.T) {
	base := &option.VLESSOutboundOptions{}
	child := &option.VLESSOutboundOptions{}
	child.Detour = "base"
	canonical := []option.Outbound{
		{Tag: "base", Options: base},
		{Tag: "child", Options: child},
	}
	prepared := cloneProviderOutbounds(canonical)
	providerParser.PrefixProviderDetours("provider", "", prepared)

	require.Equal(t, "base", child.Detour)
	preparedChild := prepared[1].Options.(*option.VLESSOutboundOptions)
	require.Equal(t, "provider_base", preparedChild.Detour)
}

func TestProviderOutboundTagUsesAdditionalPrefixInsteadOfProviderTag(t *testing.T) {
	adapter := Adapter{
		providerTag:      "provider",
		additionalPrefix: "[custom] ",
		additionalSuffix: " suffix",
	}
	outbounds := []option.Outbound{{Tag: "node"}, {Tag: "node"}}
	adapter.NormalizeProviderTags(outbounds)

	require.Equal(t, "[custom] node suffix", adapter.providerOutboundTag(outbounds[0].Tag, 0))
	require.Equal(t, "[custom] node suffix (2)", adapter.providerOutboundTag(outbounds[1].Tag, 1))
}

func TestProviderHealthCheckDiscardsReplacedOutboundResultAndChecksLatest(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	releaseOld := make(chan struct{})
	oldOutbound := &providerHealthTestOutbound{
		tag:                "provider_node",
		started:            make(chan struct{}),
		release:            releaseOld,
		ignoreCancellation: true,
	}
	skippedOutbound := &providerHealthTestOutbound{tag: "provider_node", started: make(chan struct{})}
	releaseLatest := make(chan struct{})
	latestOutbound := &providerHealthTestOutbound{
		tag:     "provider_node",
		started: make(chan struct{}),
		release: releaseLatest,
	}
	manager := newProviderHealthTestManager(oldOutbound)
	history := urltest.NewHistoryStorage()
	initialHistory := &urltest.History{Time: time.Unix(1, 0), Delay: 777}
	history.StoreURLTestHistory(oldOutbound.Tag(), initialHistory)
	provider := &Adapter{
		ctx:            ctx,
		cancel:         cancel,
		outbound:       manager,
		logger:         log.NewNOPFactory().Logger(),
		enabled:        true,
		link:           "http://example.com",
		timeout:        time.Minute,
		history:        history,
		outbounds:      []adapter.Outbound{oldOutbound},
		outboundsByTag: map[string]adapter.Outbound{oldOutbound.Tag(): oldOutbound},
	}
	defer provider.Close()

	provider.scheduleHealthCheck(ctx, false)
	<-oldOutbound.started

	manager.set(skippedOutbound)
	provider.publishOutbounds(
		[]adapter.Outbound{skippedOutbound},
		map[string]adapter.Outbound{skippedOutbound.Tag(): skippedOutbound},
	)
	manager.set(latestOutbound)
	provider.publishOutbounds(
		[]adapter.Outbound{latestOutbound},
		map[string]adapter.Outbound{latestOutbound.Tag(): latestOutbound},
	)

	close(releaseOld)
	<-latestOutbound.started
	select {
	case <-skippedOutbound.started:
		t.Fatal("intermediate outbound was health checked")
	default:
	}
	require.Same(t, initialHistory, history.LoadURLTestHistory(oldOutbound.Tag()))

	close(releaseLatest)
	provider.healthWait.Wait()
	updatedHistory := history.LoadURLTestHistory(latestOutbound.Tag())
	require.NotNil(t, updatedHistory)
	require.NotEqual(t, initialHistory.Time, updatedHistory.Time)
}

func TestProviderCloseWaitsForActiveHealthCheck(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	release := make(chan struct{})
	outbound := &providerHealthTestOutbound{
		tag:                "provider_node",
		started:            make(chan struct{}),
		release:            release,
		ignoreCancellation: true,
	}
	manager := newProviderHealthTestManager(outbound)
	provider := &Adapter{
		ctx:            ctx,
		cancel:         cancel,
		outbound:       manager,
		logger:         log.NewNOPFactory().Logger(),
		link:           "http://example.com",
		timeout:        time.Minute,
		history:        urltest.NewHistoryStorage(),
		outbounds:      []adapter.Outbound{outbound},
		outboundsByTag: map[string]adapter.Outbound{outbound.Tag(): outbound},
	}
	provider.scheduleHealthCheck(ctx, false)
	<-outbound.started

	closed := make(chan error, 1)
	go func() {
		closed <- provider.Close()
	}()
	select {
	case err := <-closed:
		t.Fatalf("provider closed before the active health check exited: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	require.NoError(t, <-closed)
}
