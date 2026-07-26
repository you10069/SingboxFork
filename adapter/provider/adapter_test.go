package provider

import (
	"testing"

	"github.com/sagernet/sing-box/option"
	providerParser "github.com/sagernet/sing-box/provider/parser"
	"github.com/stretchr/testify/require"
)

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
