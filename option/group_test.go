package option

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSelectorOutboundFilterOptions(t *testing.T) {
	var options SelectorOutboundOptions
	require.NoError(t, json.Unmarshal([]byte(`{
		"outbounds": ["manual"],
		"include_all_outbounds": true,
		"include": "(?i)us",
		"exclude": "slow",
		"exclude_type": "trojan"
	}`), &options))
	require.Equal(t, []string{"manual"}, options.Outbounds)
	require.True(t, options.IncludeAllOutbounds)
	require.Equal(t, "(?i)us", options.Include.Build().String())
	require.Equal(t, "slow", options.Exclude.Build().String())
	require.Equal(t, "trojan", options.ExcludeType.Build().String())
}
