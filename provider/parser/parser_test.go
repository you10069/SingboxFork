package parser

import (
	"context"
	"encoding/base64"
	"testing"
	"time"

	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/service"
	"github.com/stretchr/testify/require"
)

func TestNormalizeProviderTags(t *testing.T) {
	outbounds := []option.Outbound{
		{Tag: ""},
		{Tag: "node"},
		{Tag: "node (2)"},
		{Tag: "node"},
		{Tag: ""},
	}
	NormalizeProviderTags(outbounds, "", "")
	require.Equal(t, []string{"0", "node", "node (2)", "node (3)", "4"}, []string{
		outbounds[0].Tag,
		outbounds[1].Tag,
		outbounds[2].Tag,
		outbounds[3].Tag,
		outbounds[4].Tag,
	})
}

func TestNormalizeProviderTagsWithAffixes(t *testing.T) {
	base := &option.VLESSOutboundOptions{}
	child := &option.VLESSOutboundOptions{}
	child.Detour = "node"
	outbounds := []option.Outbound{
		{Tag: "node", Options: base},
		{Tag: "child", Options: child},
		{Tag: "node", Options: &option.VLESSOutboundOptions{}},
	}
	NormalizeProviderTags(outbounds, "[custom] ", " suffix")
	require.Equal(t, []string{"[custom] node suffix", "[custom] child suffix", "[custom] node suffix (2)"}, []string{
		outbounds[0].Tag,
		outbounds[1].Tag,
		outbounds[2].Tag,
	})
	require.Equal(t, "[custom] node suffix", child.Detour)

	PrefixProviderDetours("subscription", "[custom] ", outbounds)
	require.Equal(t, "[custom] node suffix", child.Detour)
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
	PrefixProviderDetours("subscription", "", outbounds)
	require.Equal(t, "subscription_base", child.Detour)
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

func TestParseClashTUICHeartbeatInterval(t *testing.T) {
	outbounds, err := ParseClashSubscription(context.Background(), `
proxies:
  - name: tuic
    type: tuic
    server: example.com
    port: 443
    uuid: 00000000-0000-0000-0000-000000000000
    password: password
    heartbeat-interval: 1000
`)
	require.NoError(t, err)
	require.Len(t, outbounds, 1)
	options, loaded := outbounds[0].Options.(*option.TUICOutboundOptions)
	require.True(t, loaded)
	require.Equal(t, time.Second, time.Duration(options.Heartbeat))
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

func TestParseVMessLinkExplicitTLSFields(t *testing.T) {
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{
		"v":"2",
		"ps":"vmess",
		"add":"example.com",
		"port":"443",
		"id":"00000000-0000-0000-0000-000000000000",
		"aid":"0",
		"scy":"auto",
		"tls":"tls",
		"sni":"preferred.example.com",
		"alpn":"h2, http/1.1"
	}`))
	outbound, err := ParseSubscriptionLink("vmess://" + payload)
	require.NoError(t, err)
	options, loaded := outbound.Options.(*option.VMessOutboundOptions)
	require.True(t, loaded)
	require.NotNil(t, options.TLS)
	require.Equal(t, "preferred.example.com", options.TLS.ServerName)
	require.Equal(t, []string{"h2", "http/1.1"}, []string(options.TLS.ALPN))
}

func TestParseVMessLinkRejectsShortInputWithoutPanic(t *testing.T) {
	for _, link := range []string{
		"",
		"v",
		"vmess:",
		"vmess:/",
		"vmess://",
	} {
		require.NotPanics(t, func() {
			_, err := parseVMessLink(link)
			require.Error(t, err)
		}, link)
	}
}

func TestParseVMessURICompatibility(t *testing.T) {
	outbound, err := parseVMessLink("vmess://00000000-0000-0000-0000-000000000000@example.com:443?encryption=auto#vmess-uri")
	require.NoError(t, err)
	require.Equal(t, "vmess-uri", outbound.Tag)
	options, loaded := outbound.Options.(*option.VMessOutboundOptions)
	require.True(t, loaded)
	require.Equal(t, "example.com", options.Server)
	require.Equal(t, uint16(443), options.ServerPort)
	require.Equal(t, "00000000-0000-0000-0000-000000000000", options.UUID)
	require.Equal(t, "auto", options.Security)
}

func TestParseVLESSGRPCServiceNameDoesNotOverrideSNI(t *testing.T) {
	outbound, err := ParseSubscriptionLink("vless://00000000-0000-0000-0000-000000000000@example.com:443?type=grpc&serviceName=grpc-service&security=tls&sni=preferred.example.com")
	require.NoError(t, err)
	options, loaded := outbound.Options.(*option.VLESSOutboundOptions)
	require.True(t, loaded)
	require.NotNil(t, options.TLS)
	require.Equal(t, "preferred.example.com", options.TLS.ServerName)
	require.NotNil(t, options.Transport)
	require.Equal(t, "grpc-service", options.Transport.GRPCOptions.ServiceName)
}

func TestParseTrojanGRPCServiceName(t *testing.T) {
	outbound, err := ParseSubscriptionLink("trojan://password@example.com:443?type=grpc&serviceName=grpc-service&sni=preferred.example.com")
	require.NoError(t, err)
	options, loaded := outbound.Options.(*option.TrojanOutboundOptions)
	require.True(t, loaded)
	require.NotNil(t, options.TLS)
	require.Equal(t, "preferred.example.com", options.TLS.ServerName)
	require.NotNil(t, options.Transport)
	require.Equal(t, "grpc-service", options.Transport.GRPCOptions.ServiceName)
}

func TestParseTrojanLegacyGRPCServiceName(t *testing.T) {
	outbound, err := ParseSubscriptionLink("trojan://password@example.com:443?type=grpc&grpc-service-name=legacy-service")
	require.NoError(t, err)
	options, loaded := outbound.Options.(*option.TrojanOutboundOptions)
	require.True(t, loaded)
	require.NotNil(t, options.Transport)
	require.Equal(t, "legacy-service", options.Transport.GRPCOptions.ServiceName)
}

func TestParseHysteria2SNI(t *testing.T) {
	outbound, err := ParseSubscriptionLink("hy2://password@1.2.3.4:443?sni=example.com")
	require.NoError(t, err)
	options, loaded := outbound.Options.(*option.Hysteria2OutboundOptions)
	require.True(t, loaded)
	require.NotNil(t, options.TLS)
	require.Equal(t, "example.com", options.TLS.ServerName)
}

type parserTestOutboundRegistry struct{}

func (parserTestOutboundRegistry) CreateOptions(outboundType string) (any, bool) {
	switch outboundType {
	case "vless":
		return new(option.VLESSOutboundOptions), true
	case "direct":
		return new(option.DirectOutboundOptions), true
	default:
		return nil, false
	}
}

func parserTestContext() context.Context {
	ctx := service.ContextWithDefaultRegistry(context.Background())
	service.MustRegister[option.OutboundOptionsRegistry](ctx, parserTestOutboundRegistry{})
	return ctx
}

func TestParseBoxSubscriptionTypeSafety(t *testing.T) {
	ctx := parserTestContext()
	for _, content := range []string{
		`{"outbounds":{}}`,
		`{"outbounds":[1]}`,
		`{"outbounds":[{"type":1}]}`,
	} {
		require.NotPanics(t, func() {
			_, err := ParseBoxSubscriptionDetailed(ctx, content)
			require.Error(t, err)
		})
	}
}

func TestParseBoxSubscriptionSkipsUnknownType(t *testing.T) {
	result, err := ParseBoxSubscriptionDetailed(parserTestContext(), `{
  "outbounds": [
    {"type":"anytls","tag":"future"},
    {"type":"vless","tag":"supported","server":"example.com","server_port":443,"uuid":"00000000-0000-0000-0000-000000000000"}
  ]
}`)
	require.NoError(t, err)
	require.Len(t, result.Outbounds, 1)
	require.Equal(t, "supported", result.Outbounds[0].Tag)
	require.Equal(t, []SkippedOutbound{{Tag: "future", Type: "anytls", Reason: "unsupported by current sing-box version"}}, result.Skipped)
}

func TestParseBoxSubscriptionRejectsMalformedSupportedType(t *testing.T) {
	_, err := ParseBoxSubscriptionDetailed(parserTestContext(), `{
  "outbounds": [
    {"type":"vless","tag":"broken","server":"example.com","server_port":"invalid"}
  ]
}`)
	require.Error(t, err)
}

func TestParseRawSubscriptionStrictSupportedLink(t *testing.T) {
	_, err := ParseRawSubscriptionDetailed(context.Background(), "vless://broken")
	require.Error(t, err)
}

func TestParseRawSubscriptionSkipsUnknownScheme(t *testing.T) {
	result, err := ParseRawSubscriptionDetailed(context.Background(), "anytls://example\nvless://00000000-0000-0000-0000-000000000000@example.com:443#ok")
	require.NoError(t, err)
	require.Len(t, result.Outbounds, 1)
	require.Len(t, result.Skipped, 1)
	require.Equal(t, "anytls", result.Skipped[0].Type)
}

func TestDecodeBase64URLSafeReturnsError(t *testing.T) {
	content := "not valid base64!"
	decoded, err := DecodeBase64URLSafe(content)
	require.Error(t, err)
	require.Equal(t, content, decoded)
}

func TestParseClashSubscriptionRejectsMissingType(t *testing.T) {
	_, err := ParseClashSubscriptionDetailed(context.Background(), `
proxies:
  - name: broken
    server: example.com
    port: 443
`)
	require.Error(t, err)
}

func TestParseSubscriptionRecoversParserPanic(t *testing.T) {
	originalParsers := subscriptionParsers
	subscriptionParsers = []subscriptionParser{
		func(context.Context, string) (SubscriptionResult, error) {
			panic("broken parser")
		},
	}
	defer func() { subscriptionParsers = originalParsers }()

	require.NotPanics(t, func() {
		_, err := ParseSubscriptionDetailed(context.Background(), "content")
		require.Error(t, err)
		require.Contains(t, err.Error(), "subscription parser panic")
	})
}

func TestValidateSkippedDependencies(t *testing.T) {
	child := &option.VLESSOutboundOptions{}
	child.Detour = "future"
	err := ValidateSkippedDependencies(
		[]option.Outbound{{Type: "vless", Tag: "child", Options: child}},
		[]SkippedOutbound{{Tag: "future", Type: "anytls", Reason: "unsupported"}},
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "depends on skipped outbound")
}
