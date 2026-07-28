package box_test

import (
	"context"
	"regexp"
	"testing"

	"github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/adapter"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/include"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/json/badoption"
	"github.com/sagernet/sing/service"
	"github.com/stretchr/testify/require"
)

func TestSelectorFilterIntegration(t *testing.T) {
	ctx, cancel := context.WithCancel(include.Context(context.Background()))
	instance, err := box.New(box.Options{
		Context: ctx,
		Options: option.Options{
			Outbounds: []option.Outbound{
				{
					Type: C.TypeSelector,
					Tag:  "selector",
					Options: &option.SelectorOutboundOptions{
						Outbounds:           []string{"manual"},
						IncludeAllOutbounds: true,
						Include:             selectorFilterRegexp("^US-"),
						Exclude:             selectorFilterRegexp("slow"),
						ExcludeType:         selectorFilterRegexp("^socks$"),
						Default:             "US-fast",
					},
				},
				{Type: C.TypeDirect, Tag: "manual", Options: &option.DirectOutboundOptions{}},
				{Type: C.TypeDirect, Tag: "US-fast", Options: &option.DirectOutboundOptions{}},
				{Type: C.TypeDirect, Tag: "HK-fast", Options: &option.DirectOutboundOptions{}},
				{Type: C.TypeDirect, Tag: "US-slow", Options: &option.DirectOutboundOptions{}},
				{
					Type: C.TypeSOCKS,
					Tag:  "US-socks",
					Options: &option.SOCKSOutboundOptions{
						ServerOptions: option.ServerOptions{
							Server:     "127.0.0.1",
							ServerPort: 1080,
						},
					},
				},
			},
			Route: &option.RouteOptions{Final: "selector"},
		},
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		instance.Close()
		cancel()
	})
	require.NoError(t, instance.Start())

	outboundManager := service.FromContext[adapter.OutboundManager](ctx)
	selectorOutbound, loaded := outboundManager.Outbound("selector")
	require.True(t, loaded)
	selector := selectorOutbound.(adapter.OutboundGroup)
	require.Equal(t, []string{"manual", "US-fast"}, selector.All())
	require.Equal(t, "US-fast", selector.Now())
}

func TestSelectorFilterDependencyCycle(t *testing.T) {
	ctx, cancel := context.WithCancel(include.Context(context.Background()))
	instance, err := box.New(box.Options{
		Context: ctx,
		Options: option.Options{
			Outbounds: []option.Outbound{
				{
					Type: C.TypeSelector,
					Tag:  "selector",
					Options: &option.SelectorOutboundOptions{
						IncludeAllOutbounds: true,
						Include:             selectorFilterRegexp("^US$"),
					},
				},
				{
					Type: C.TypeSOCKS,
					Tag:  "US",
					Options: &option.SOCKSOutboundOptions{
						DialerOptions: option.DialerOptions{Detour: "selector"},
						ServerOptions: option.ServerOptions{
							Server:     "127.0.0.1",
							ServerPort: 1080,
						},
					},
				},
			},
			Route: &option.RouteOptions{Final: "selector"},
		},
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		instance.Close()
		cancel()
	})
	require.ErrorContains(t, instance.Start(), "circular outbound dependency")
}

func selectorFilterRegexp(expression string) *badoption.Regexp {
	compiled := badoption.Regexp(*regexp.MustCompile(expression))
	return &compiled
}
