package parser

import (
	"context"
	"testing"

	"github.com/sagernet/sing-box/option"
	"github.com/stretchr/testify/require"
)

func TestNormalizeProviderTags(t *testing.T) {
	outbounds := []option.Outbound{
		{Tag: ""},
		{Tag: "node"},
		{Tag: "node"},
		{Tag: "node-2"},
		{Tag: ""},
	}
	NormalizeProviderTags(outbounds)
	require.Equal(t, []string{"0", "node", "node-2", "node-2-2", "4"}, []string{
		outbounds[0].Tag,
		outbounds[1].Tag,
		outbounds[2].Tag,
		outbounds[3].Tag,
		outbounds[4].Tag,
	})
}

func TestPrefixProviderDetours(t *testing.T) {
	base := &option.VLESSOutboundOptions{}
	child := &option.VLESSOutboundOptions{}
	child.Detour = "base"
	external := &option.VLESSOutboundOptions{}
	external.Detour = "global"
	outbounds := []option.Outbound{
		{Tag: "base", Options: base},
		{Tag: "child", Options: child},
		{Tag: "external", Options: external},
	}
	PrefixProviderDetours("subscription", outbounds)
	require.Equal(t, "subscription/base", child.Detour)
	require.Equal(t, "global", external.Detour)
}

func TestParseClashTLSFalse(t *testing.T) {
	outbounds, err := ParseClashSubscription(context.Background(), `
proxies:
  - name: plain-vmess
    type: vmess
    server: example.com
    port: 443
    uuid: 00000000-0000-0000-0000-000000000000
    alterId: 0
    cipher: auto
    tls: false
`)
	require.NoError(t, err)
	require.Len(t, outbounds, 1)
	options, loaded := outbounds[0].Options.(*option.VMessOutboundOptions)
	require.True(t, loaded)
	require.Nil(t, options.TLS)
}

func TestParseClashHysteriaFirstPort(t *testing.T) {
	outbounds, err := ParseClashSubscription(context.Background(), `
proxies:
  - name: hy
    type: hysteria
    server: example.com
    ports: 443-445,8443
    auth-str: password
    up: 10 Mbps
    down: 50 Mbps
`)
	require.NoError(t, err)
	require.Len(t, outbounds, 1)
	options, loaded := outbounds[0].Options.(*option.HysteriaOutboundOptions)
	require.True(t, loaded)
	require.Equal(t, uint16(443), options.ServerPort)
}
