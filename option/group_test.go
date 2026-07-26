package option

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGroupAutomaticCollectionOptions(t *testing.T) {
	var options SelectorOutboundOptions
	err := json.Unmarshal([]byte(`{
		"outbounds":["direct"],
		"include_all":true,
		"include_all_outbounds":true,
		"use_all_providers":true,
		"include":"HK|JP",
		"exclude":"expire",
		"exclude_all":true,
		"exclude_type":"trojan|tuic",
		"exclude_type_all":true
	}`), &options)
	require.NoError(t, err)
	require.True(t, options.IncludeAll)
	require.True(t, options.IncludeAllOutbounds)
	require.True(t, options.UseAllProviders)
	require.True(t, options.ExcludeAll)
	require.True(t, options.ExcludeTypeAll)
	require.NotNil(t, options.Include)
	require.NotNil(t, options.Exclude)
	require.NotNil(t, options.ExcludeType)
}
