package local

import (
	"context"
	"path/filepath"
	"testing"

	adapterProvider "github.com/sagernet/sing-box/adapter/provider"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/stretchr/testify/require"
)

func TestReloadIgnoredAfterClose(t *testing.T) {
	ctx := context.Background()
	logFactory := log.NewNOPFactory()
	logger := logFactory.NewLogger("test")
	provider := &ProviderLocal{
		Adapter: adapterProvider.NewAdapter(
			ctx,
			nil,
			nil,
			logFactory,
			logger,
			"test",
			C.ProviderTypeLocal,
			option.ProviderHealthCheckOptions{},
			"",
			"",
		),
		ctx:    ctx,
		logger: logger,
	}

	require.NoError(t, provider.Close())
	require.True(t, provider.closed)
	require.NoError(t, provider.reloadFile(filepath.Join(t.TempDir(), "missing-provider.yaml")))
}
