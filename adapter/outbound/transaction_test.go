package outbound

import (
	"context"
	"errors"
	"net"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/log"
	M "github.com/sagernet/sing/common/metadata"
	"github.com/stretchr/testify/require"
)

type transactionTestOptions struct {
	Dependencies []string
	Fail         bool
	FailStart    bool
}

type transactionTestOutbound struct {
	tag          string
	dependencies []string
	failStart    bool
	closed       bool
}

func (o *transactionTestOutbound) Type() string      { return "test" }
func (o *transactionTestOutbound) Tag() string       { return o.tag }
func (o *transactionTestOutbound) Network() []string { return []string{"tcp", "udp"} }
func (o *transactionTestOutbound) Dependencies() []string {
	return append([]string(nil), o.dependencies...)
}
func (o *transactionTestOutbound) DialContext(context.Context, string, M.Socksaddr) (net.Conn, error) {
	return nil, errors.New("not implemented")
}
func (o *transactionTestOutbound) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	return nil, errors.New("not implemented")
}
func (o *transactionTestOutbound) Start(stage adapter.StartStage) error {
	if o.failStart && stage == adapter.StartStateStart {
		return errors.New("requested start failure")
	}
	return nil
}
func (o *transactionTestOutbound) Close() error { o.closed = true; return nil }

type transactionTestRegistry struct{}

func (transactionTestRegistry) CreateOptions(string) (any, bool) {
	return new(transactionTestOptions), true
}
func (transactionTestRegistry) CreateOutbound(_ context.Context, _ adapter.Router, _ log.ContextLogger, tag string, _ string, rawOptions any) (adapter.Outbound, error) {
	options := rawOptions.(*transactionTestOptions)
	if options.Fail {
		return nil, errors.New("requested failure")
	}
	return &transactionTestOutbound{tag: tag, dependencies: options.Dependencies, failStart: options.FailStart}, nil
}

type transactionTestEndpointManager struct{}

func (transactionTestEndpointManager) Start(adapter.StartStage) error { return nil }
func (transactionTestEndpointManager) Close() error                   { return nil }
func (transactionTestEndpointManager) Endpoints() []adapter.Endpoint  { return nil }
func (transactionTestEndpointManager) Get(string) (adapter.Endpoint, bool) {
	return nil, false
}
func (transactionTestEndpointManager) Remove(string) error { return errors.New("not found") }
func (transactionTestEndpointManager) Create(context.Context, adapter.Router, log.ContextLogger, string, string, any) error {
	return errors.New("not implemented")
}

func newTransactionTestManager() *Manager {
	return NewManager(log.NewNOPFactory().NewLogger("test"), transactionTestRegistry{}, transactionTestEndpointManager{}, "")
}

func TestPrepareOutboundsDoesNotPublishPartialState(t *testing.T) {
	manager := newTransactionTestManager()
	logger := log.NewNOPFactory().NewLogger("test")
	transaction, err := manager.PrepareOutbounds(nil, []adapter.OutboundBatchItem{
		{Context: context.Background(), Logger: logger, Tag: "a", Type: "test", Options: &transactionTestOptions{}},
		{Context: context.Background(), Logger: logger, Tag: "b", Type: "test", Options: &transactionTestOptions{Fail: true}},
	})
	require.Error(t, err)
	require.Nil(t, transaction)
	_, exists := manager.Outbound("a")
	require.False(t, exists)
}

func TestOutboundTransactionPublishesAtomically(t *testing.T) {
	manager := newTransactionTestManager()
	logger := log.NewNOPFactory().NewLogger("test")
	transaction, err := manager.PrepareOutbounds(nil, []adapter.OutboundBatchItem{
		{Context: context.Background(), Logger: logger, Tag: "base", Type: "test", Options: &transactionTestOptions{}},
		{Context: context.Background(), Logger: logger, Tag: "child", Type: "test", Options: &transactionTestOptions{Dependencies: []string{"base"}}},
	})
	require.NoError(t, err)
	_, exists := manager.Outbound("base")
	require.False(t, exists)
	replaced, err := transaction.Commit(nil)
	require.NoError(t, err)
	require.Empty(t, replaced)
	_, exists = manager.Outbound("base")
	require.True(t, exists)
	_, exists = manager.Outbound("child")
	require.True(t, exists)
}

func TestOutboundTransactionCacheHookFailureKeepsOldState(t *testing.T) {
	manager := newTransactionTestManager()
	logger := log.NewNOPFactory().NewLogger("test")
	require.NoError(t, manager.Create(context.Background(), nil, logger, "node", "test", &transactionTestOptions{}))
	oldOutbound, exists := manager.Outbound("node")
	require.True(t, exists)
	transaction, err := manager.PrepareOutbounds([]string{"node"}, []adapter.OutboundBatchItem{
		{Context: context.Background(), Logger: logger, Tag: "node", Type: "test", Options: &transactionTestOptions{}},
	})
	require.NoError(t, err)
	_, err = transaction.Commit(func() error { return errors.New("cache failure") })
	require.Error(t, err)
	currentOutbound, exists := manager.Outbound("node")
	require.True(t, exists)
	require.Same(t, oldOutbound, currentOutbound)
}

func TestPrepareOutboundsStartFailureDoesNotPublish(t *testing.T) {
	manager := newTransactionTestManager()
	logger := log.NewNOPFactory().NewLogger("test")
	require.NoError(t, manager.Start(adapter.StartStateInitialize))
	require.NoError(t, manager.Start(adapter.StartStateStart))

	transaction, err := manager.PrepareOutbounds(nil, []adapter.OutboundBatchItem{
		{Context: context.Background(), Logger: logger, Tag: "base", Type: "test", Options: &transactionTestOptions{}},
		{Context: context.Background(), Logger: logger, Tag: "child", Type: "test", Options: &transactionTestOptions{Dependencies: []string{"base"}, FailStart: true}},
	})
	require.Error(t, err)
	require.Nil(t, transaction)
	_, exists := manager.Outbound("base")
	require.False(t, exists)
	_, exists = manager.Outbound("child")
	require.False(t, exists)
}

func TestManagerCloseCleansUnstartedOutbounds(t *testing.T) {
	manager := newTransactionTestManager()
	logger := log.NewNOPFactory().NewLogger("test")
	require.NoError(t, manager.Create(context.Background(), nil, logger, "node", "test", &transactionTestOptions{}))
	outbound, exists := manager.Outbound("node")
	require.True(t, exists)
	testOutbound := outbound.(*transactionTestOutbound)

	require.NoError(t, manager.Close())
	require.True(t, testOutbound.closed)
	_, exists = manager.Outbound("node")
	require.False(t, exists)
}

func TestManagerCreateReplacesImplicitDefault(t *testing.T) {
	manager := newTransactionTestManager()
	logger := log.NewNOPFactory().NewLogger("test")
	require.NoError(t, manager.Create(context.Background(), nil, logger, "node", "test", &transactionTestOptions{}))
	oldDefault := manager.Default()
	require.NotNil(t, oldDefault)

	require.NoError(t, manager.Create(context.Background(), nil, logger, "node", "test", &transactionTestOptions{}))
	newDefault := manager.Default()
	current, exists := manager.Outbound("node")

	require.True(t, exists)
	require.NotNil(t, newDefault)
	require.NotSame(t, oldDefault, newDefault)
	require.Same(t, current, newDefault)
}
