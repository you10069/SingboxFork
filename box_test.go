package box

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	adapterProvider "github.com/sagernet/sing-box/adapter/provider"
	"github.com/sagernet/sing-box/include"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/x/list"
	"github.com/stretchr/testify/require"
)

type boxProviderCleanupTestProvider struct {
	tag        string
	closeCount int
	closeErr   error
}

func (*boxProviderCleanupTestProvider) Type() string  { return "test" }
func (p *boxProviderCleanupTestProvider) Tag() string { return p.tag }
func (*boxProviderCleanupTestProvider) Outbounds() []adapter.Outbound {
	return nil
}
func (*boxProviderCleanupTestProvider) Outbound(string) (adapter.Outbound, bool) {
	return nil, false
}
func (*boxProviderCleanupTestProvider) UpdatedAt() time.Time {
	return time.Time{}
}
func (*boxProviderCleanupTestProvider) HealthCheck(context.Context) (map[string]uint16, error) {
	return nil, nil
}
func (*boxProviderCleanupTestProvider) RegisterCallback(adapter.ProviderUpdateCallback) *list.Element[adapter.ProviderUpdateCallback] {
	return nil
}
func (*boxProviderCleanupTestProvider) UnregisterCallback(*list.Element[adapter.ProviderUpdateCallback]) {
}
func (p *boxProviderCleanupTestProvider) Close() error {
	p.closeCount++
	return p.closeErr
}

func newBoxProviderCleanupTestOptions(
	t *testing.T,
	providerRegistry *adapterProvider.Registry,
	providers ...option.Provider,
) Options {
	t.Helper()
	ctx := Context(
		context.Background(),
		include.InboundRegistry(),
		include.OutboundRegistry(),
		include.EndpointRegistry(),
		providerRegistry,
	)
	return Options{
		Context: ctx,
		Options: option.Options{
			Providers: providers,
		},
	}
}

func TestNewCleansCreatedProvidersWhenLaterProviderFails(t *testing.T) {
	providerRegistry := adapterProvider.NewRegistry()
	var created *boxProviderCleanupTestProvider
	adapterProvider.Register[struct{}](providerRegistry, "success", func(_ context.Context, _ adapter.Router, _ log.Factory, tag string, _ struct{}) (adapter.Provider, error) {
		created = &boxProviderCleanupTestProvider{tag: tag}
		return created, nil
	})
	createErr := errors.New("provider constructor failed")
	adapterProvider.Register[struct{}](providerRegistry, "failure", func(context.Context, adapter.Router, log.Factory, string, struct{}) (adapter.Provider, error) {
		return nil, createErr
	})

	instance, err := New(newBoxProviderCleanupTestOptions(
		t,
		providerRegistry,
		option.Provider{Type: "success", Tag: "first", Options: new(struct{})},
		option.Provider{Type: "failure", Tag: "second", Options: new(struct{})},
	))

	require.Nil(t, instance)
	require.ErrorIs(t, err, createErr)
	require.NotNil(t, created)
	require.Equal(t, 1, created.closeCount)
}

func TestNewKeepsProvidersOpenUntilBoxClose(t *testing.T) {
	providerRegistry := adapterProvider.NewRegistry()
	var created *boxProviderCleanupTestProvider
	adapterProvider.Register[struct{}](providerRegistry, "success", func(_ context.Context, _ adapter.Router, _ log.Factory, tag string, _ struct{}) (adapter.Provider, error) {
		created = &boxProviderCleanupTestProvider{tag: tag}
		return created, nil
	})

	instance, err := New(newBoxProviderCleanupTestOptions(
		t,
		providerRegistry,
		option.Provider{Type: "success", Tag: "first", Options: new(struct{})},
	))

	require.NoError(t, err)
	require.NotNil(t, instance)
	require.NotNil(t, created)
	require.Zero(t, created.closeCount)
	require.NoError(t, instance.Close())
	require.Equal(t, 1, created.closeCount)
}

func TestNewPreservesInitializationAndProviderCleanupErrors(t *testing.T) {
	providerRegistry := adapterProvider.NewRegistry()
	cleanupErr := errors.New("provider cleanup failed")
	adapterProvider.Register[struct{}](providerRegistry, "success", func(_ context.Context, _ adapter.Router, _ log.Factory, tag string, _ struct{}) (adapter.Provider, error) {
		return &boxProviderCleanupTestProvider{tag: tag, closeErr: cleanupErr}, nil
	})
	createErr := errors.New("provider constructor failed")
	adapterProvider.Register[struct{}](providerRegistry, "failure", func(context.Context, adapter.Router, log.Factory, string, struct{}) (adapter.Provider, error) {
		return nil, createErr
	})

	instance, err := New(newBoxProviderCleanupTestOptions(
		t,
		providerRegistry,
		option.Provider{Type: "success", Tag: "first", Options: new(struct{})},
		option.Provider{Type: "failure", Tag: "second", Options: new(struct{})},
	))

	require.Nil(t, instance)
	require.ErrorIs(t, err, createErr)
	require.ErrorIs(t, err, cleanupErr)
}
