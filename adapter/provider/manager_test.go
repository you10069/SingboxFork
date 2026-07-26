package provider

import (
	"context"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing/common/x/list"
	"github.com/stretchr/testify/require"
)

type managerTestProvider struct {
	tag    string
	closed bool
}

func (*managerTestProvider) Type() string                                           { return "test" }
func (p *managerTestProvider) Tag() string                                          { return p.tag }
func (*managerTestProvider) Outbounds() []adapter.Outbound                          { return nil }
func (*managerTestProvider) Outbound(string) (adapter.Outbound, bool)               { return nil, false }
func (*managerTestProvider) UpdatedAt() time.Time                                   { return time.Time{} }
func (*managerTestProvider) HealthCheck(context.Context) (map[string]uint16, error) { return nil, nil }
func (*managerTestProvider) RegisterCallback(adapter.ProviderUpdateCallback) *list.Element[adapter.ProviderUpdateCallback] {
	return nil
}
func (*managerTestProvider) UnregisterCallback(*list.Element[adapter.ProviderUpdateCallback]) {}
func (p *managerTestProvider) Close() error                                                   { p.closed = true; return nil }

type managerTestRegistry struct {
	created []*managerTestProvider
}

func (*managerTestRegistry) CreateOptions(string) (any, bool) { return new(struct{}), true }
func (r *managerTestRegistry) CreateProvider(_ context.Context, _ adapter.Router, _ log.Factory, tag string, _ string, _ any) (adapter.Provider, error) {
	provider := &managerTestProvider{tag: tag}
	r.created = append(r.created, provider)
	return provider, nil
}

func TestManagerRejectsDuplicateProviderTag(t *testing.T) {
	registry := new(managerTestRegistry)
	manager := NewManager(log.NewNOPFactory().NewLogger("test"), registry)
	factory := log.NewNOPFactory()
	require.NoError(t, manager.Create(context.Background(), nil, factory, "subscription", "test", nil))
	require.Error(t, manager.Create(context.Background(), nil, factory, "subscription", "test", nil))
	require.Len(t, registry.created, 1)
}

func TestManagerCloseCleansUnstartedProvider(t *testing.T) {
	registry := new(managerTestRegistry)
	manager := NewManager(log.NewNOPFactory().NewLogger("test"), registry)
	require.NoError(t, manager.Create(context.Background(), nil, log.NewNOPFactory(), "subscription", "test", nil))
	require.NoError(t, manager.Close())
	require.True(t, registry.created[0].closed)
	require.Empty(t, manager.Providers())
}

type managerTestPreparation struct {
	committed bool
	aborted   bool
}

func (p *managerTestPreparation) Commit() { p.committed = true }
func (p *managerTestPreparation) Abort()  { p.aborted = true }

func TestPrepareGroupUpdatesAbortsEarlierPreparation(t *testing.T) {
	adapterValue := &Adapter{providerTag: "subscription"}
	first := new(managerTestPreparation)
	adapterValue.RegisterPrepareCallback(func(string, []adapter.Outbound) (adapter.ProviderUpdatePreparation, error) {
		return first, nil
	})
	adapterValue.RegisterPrepareCallback(func(string, []adapter.Outbound) (adapter.ProviderUpdatePreparation, error) {
		return nil, context.Canceled
	})
	preparations, err := adapterValue.prepareGroupUpdates(nil)
	require.Error(t, err)
	require.Nil(t, preparations)
	require.True(t, first.aborted)
	require.False(t, first.committed)
}
