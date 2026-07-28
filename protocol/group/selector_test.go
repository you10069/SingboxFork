package group

import (
	"context"
	"regexp"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/json/badoption"
	"github.com/sagernet/sing/service"
	"github.com/stretchr/testify/require"
)

func TestSelectorOutboundTagsExplicitOnly(t *testing.T) {
	ctx := selectorTestContext([]adapter.StaticOutboundMetadataItem{
		{Tag: "US-auto", Type: "vless"},
	})
	options := option.SelectorOutboundOptions{
		Outbounds:   []string{"manual", "manual"},
		Include:     selectorTestRegexp("^US-"),
		Exclude:     selectorTestRegexp("manual"),
		ExcludeType: selectorTestRegexp("vless"),
	}
	require.Equal(t, []string{"manual", "manual"}, selectorOutboundTags(ctx, "selector", options))
}

func TestSelectorOutboundTagsAutomaticFilters(t *testing.T) {
	ctx := selectorTestContext([]adapter.StaticOutboundMetadataItem{
		{Tag: "manual", Type: "trojan"},
		{Tag: "US-fast", Type: "vless"},
		{Tag: "HK-fast", Type: "vless"},
		{Tag: "US-slow", Type: "vless"},
		{Tag: "US-trojan", Type: "trojan"},
		{Tag: "US-fast", Type: "vless"},
		{Tag: "selector", Type: "vless"},
	})
	options := option.SelectorOutboundOptions{
		Outbounds:           []string{"manual"},
		IncludeAllOutbounds: true,
		Include:             selectorTestRegexp("^US-"),
		Exclude:             selectorTestRegexp("slow"),
		ExcludeType:         selectorTestRegexp("^trojan$"),
	}
	require.Equal(t, []string{"manual", "US-fast"}, selectorOutboundTags(ctx, "selector", options))
}

func TestSelectorOutboundTagsAutomaticWithoutMetadata(t *testing.T) {
	ctx := service.ContextWithDefaultRegistry(context.Background())
	options := option.SelectorOutboundOptions{IncludeAllOutbounds: true}
	require.Empty(t, selectorOutboundTags(ctx, "selector", options))
}

func selectorTestContext(outbounds []adapter.StaticOutboundMetadataItem) context.Context {
	ctx := service.ContextWithDefaultRegistry(context.Background())
	service.MustRegister[adapter.StaticOutboundMetadata](ctx, adapter.StaticOutboundMetadata{
		Outbounds: outbounds,
	})
	return ctx
}

func selectorTestRegexp(expression string) *badoption.Regexp {
	compiled := badoption.Regexp(*regexp.MustCompile(expression))
	return &compiled
}
