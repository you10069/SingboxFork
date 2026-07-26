package parser

import (
	"context"
	"testing"
	"time"

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

func TestParseClashVMessSNIPriority(t *testing.T) {
	outbounds, err := ParseClashSubscription(context.Background(), `
proxies:
  - name: vmess-sni
    type: vmess
    server: example.com
    port: 443
    uuid: 00000000-0000-0000-0000-000000000000
    alterId: 0
    cipher: auto
    tls: true
    sni: preferred.example.com
    servername: fallback.example.com
`)
	require.NoError(t, err)
	require.Len(t, outbounds, 1)
	options, loaded := outbounds[0].Options.(*option.VMessOutboundOptions)
	require.True(t, loaded)
	require.NotNil(t, options.TLS)
	require.Equal(t, "preferred.example.com", options.TLS.ServerName)
}

func TestParseClashVLESSSNIPriority(t *testing.T) {
	outbounds, err := ParseClashSubscription(context.Background(), `
proxies:
  - name: vless-sni
    type: vless
    server: example.com
    port: 443
    uuid: 00000000-0000-0000-0000-000000000000
    tls: true
    sni: preferred.example.com
    servername: fallback.example.com
`)
	require.NoError(t, err)
	require.Len(t, outbounds, 1)
	options, loaded := outbounds[0].Options.(*option.VLESSOutboundOptions)
	require.True(t, loaded)
	require.NotNil(t, options.TLS)
	require.Equal(t, "preferred.example.com", options.TLS.ServerName)
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

func TestParseClashHysteria2HopInterval(t *testing.T) {
	outbounds, err := ParseClashSubscription(context.Background(), `
proxies:
  - name: hy2
    type: hysteria2
    server: example.com
    port: 443
    password: password
    hop-interval: 30
`)
	require.NoError(t, err)
	require.Len(t, outbounds, 1)
	options, loaded := outbounds[0].Options.(*option.Hysteria2OutboundOptions)
	require.True(t, loaded)
	require.Equal(t, 30*time.Second, time.Duration(options.HopInterval))
}

func TestParseTrojanGRPCServiceName(t *testing.T) {
	outbound, err := ParseSubscriptionLink("trojan://password@example.com:443?type=grpc&serviceName=grpc-service")
	require.NoError(t, err)
	options, loaded := outbound.Options.(*option.TrojanOutboundOptions)
	require.True(t, loaded)
	require.NotNil(t, options.TLS)
	require.Equal(t, "example.com", options.TLS.ServerName)
	require.NotNil(t, options.Transport)
	require.Equal(t, "grpc-service", options.Transport.GRPCOptions.ServiceName)
}

func TestParseHysteria2SNI(t *testing.T) {
	outbound, err := ParseSubscriptionLink("hy2://password@1.2.3.4:443?sni=example.com")
	require.NoError(t, err)
	options, loaded := outbound.Options.(*option.Hysteria2OutboundOptions)
	require.True(t, loaded)
	require.NotNil(t, options.TLS)
	require.Equal(t, "example.com", options.TLS.ServerName)
}
