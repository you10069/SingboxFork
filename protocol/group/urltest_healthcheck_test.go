package group

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
	M "github.com/sagernet/sing/common/metadata"
	"github.com/sagernet/sing/service"
	"github.com/stretchr/testify/require"
)

type urlTestHealthTestOutbound struct {
	tag                string
	started            chan struct{}
	startOnce          sync.Once
	release            <-chan struct{}
	ignoreCancellation bool
}

func (o *urlTestHealthTestOutbound) Type() string           { return "test" }
func (o *urlTestHealthTestOutbound) Tag() string            { return o.tag }
func (o *urlTestHealthTestOutbound) Network() []string      { return []string{"tcp"} }
func (o *urlTestHealthTestOutbound) Dependencies() []string { return nil }
func (o *urlTestHealthTestOutbound) DialContext(ctx context.Context, _ string, _ M.Socksaddr) (net.Conn, error) {
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
func (*urlTestHealthTestOutbound) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	return nil, errors.New("not implemented")
}

type urlTestHealthTestManager struct {
	access    sync.RWMutex
	outbounds []adapter.Outbound
	byTag     map[string]adapter.Outbound
}

func newURLTestHealthTestManager(outbounds ...adapter.Outbound) *urlTestHealthTestManager {
	manager := &urlTestHealthTestManager{}
	manager.set(outbounds...)
	return manager
}

func (m *urlTestHealthTestManager) set(outbounds ...adapter.Outbound) {
	m.access.Lock()
	defer m.access.Unlock()
	m.outbounds = append([]adapter.Outbound(nil), outbounds...)
	m.byTag = make(map[string]adapter.Outbound, len(outbounds))
	for _, outbound := range outbounds {
		m.byTag[outbound.Tag()] = outbound
	}
}

func (*urlTestHealthTestManager) Start(adapter.StartStage) error { return nil }
func (*urlTestHealthTestManager) Close() error                   { return nil }
func (m *urlTestHealthTestManager) Outbounds() []adapter.Outbound {
	m.access.RLock()
	defer m.access.RUnlock()
	return append([]adapter.Outbound(nil), m.outbounds...)
}
func (m *urlTestHealthTestManager) Outbound(tag string) (adapter.Outbound, bool) {
	m.access.RLock()
	defer m.access.RUnlock()
	outbound, loaded := m.byTag[tag]
	return outbound, loaded
}
func (m *urlTestHealthTestManager) Default() adapter.Outbound {
	m.access.RLock()
	defer m.access.RUnlock()
	if len(m.outbounds) == 0 {
		return nil
	}
	return m.outbounds[0]
}
func (m *urlTestHealthTestManager) Remove(tag string) error {
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
func (*urlTestHealthTestManager) Create(context.Context, adapter.Router, log.ContextLogger, string, string, any) error {
	return errors.New("not implemented")
}

func newURLTestHealthTestGroup(t *testing.T, ctx context.Context, manager adapter.OutboundManager, history *urltest.HistoryStorage, outbounds ...adapter.Outbound) *URLTestGroup {
	t.Helper()
	ctx = service.ContextWithPtr(ctx, history)
	group, err := NewURLTestGroup(
		ctx,
		manager,
		log.NewNOPFactory().Logger(),
		outbounds,
		"http://example.com",
		time.Minute,
		50,
		5*time.Minute,
		false,
	)
	require.NoError(t, err)
	group.access.Lock()
	group.started = true
	group.access.Unlock()
	return group
}

func TestURLTestDiscardsReplacedOutboundResultAndChecksLatest(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	releaseOld := make(chan struct{})
	oldOutbound := &urlTestHealthTestOutbound{
		tag:                "provider_node",
		started:            make(chan struct{}),
		release:            releaseOld,
		ignoreCancellation: true,
	}
	skippedOutbound := &urlTestHealthTestOutbound{tag: "provider_node", started: make(chan struct{})}
	releaseLatest := make(chan struct{})
	latestOutbound := &urlTestHealthTestOutbound{
		tag:     "provider_node",
		started: make(chan struct{}),
		release: releaseLatest,
	}
	manager := newURLTestHealthTestManager(oldOutbound)
	history := urltest.NewHistoryStorage()
	initialHistory := &urltest.History{Time: time.Unix(1, 0), Delay: 777}
	history.StoreURLTestHistory(oldOutbound.Tag(), initialHistory)
	group := newURLTestHealthTestGroup(t, ctx, manager, history, oldOutbound)
	defer group.Close()

	group.scheduleURLTest(ctx, true, false)
	<-oldOutbound.started

	manager.set(skippedOutbound)
	group.UpdateOutbounds([]adapter.Outbound{skippedOutbound})
	manager.set(latestOutbound)
	group.UpdateOutbounds([]adapter.Outbound{latestOutbound})
	latestResult := make(chan urlTestHealthCheckResult, 1)
	group.enqueueURLTest(ctx, false, true, latestResult)

	close(releaseOld)
	<-latestOutbound.started
	select {
	case <-skippedOutbound.started:
		t.Fatal("intermediate outbound was health checked")
	default:
	}
	require.Same(t, initialHistory, history.LoadURLTestHistory(oldOutbound.Tag()))

	close(releaseLatest)
	require.NoError(t, (<-latestResult).err)
	group.healthWait.Wait()
	updatedHistory := history.LoadURLTestHistory(latestOutbound.Tag())
	require.NotNil(t, updatedHistory)
	require.NotEqual(t, initialHistory.Time, updatedHistory.Time)
	require.Same(t, latestOutbound, group.Selected("tcp"))
}

func TestURLTestCloseWaitsForActiveHealthCheck(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	release := make(chan struct{})
	outbound := &urlTestHealthTestOutbound{
		tag:                "provider_node",
		started:            make(chan struct{}),
		release:            release,
		ignoreCancellation: true,
	}
	manager := newURLTestHealthTestManager(outbound)
	group := newURLTestHealthTestGroup(t, ctx, manager, urltest.NewHistoryStorage(), outbound)
	group.scheduleURLTest(ctx, true, false)
	<-outbound.started

	closed := make(chan error, 1)
	go func() {
		closed <- group.Close()
	}()
	select {
	case err := <-closed:
		t.Fatalf("URLTest group closed before the active health check exited: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	require.NoError(t, <-closed)
}
