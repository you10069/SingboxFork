package parser

import (
	"context"
	"fmt"

	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"
	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/service"
)

type SkippedOutbound struct {
	Tag    string
	Type   string
	Reason string
}

type SubscriptionResult struct {
	Outbounds []option.Outbound
	Skipped   []SkippedOutbound
}

type subscriptionParser func(ctx context.Context, content string) (SubscriptionResult, error)

var subscriptionParsers = []subscriptionParser{
	ParseBoxSubscriptionDetailed,
	ParseClashSubscriptionDetailed,
	wrapSubscriptionParser(ParseSIP008Subscription),
	ParseRawSubscriptionDetailed,
}

func wrapSubscriptionParser(parser func(context.Context, string) ([]option.Outbound, error)) subscriptionParser {
	return func(ctx context.Context, content string) (SubscriptionResult, error) {
		outbounds, err := parser(ctx, content)
		return SubscriptionResult{Outbounds: outbounds}, err
	}
}

func callSubscriptionParser(parser subscriptionParser, ctx context.Context, content string) (result SubscriptionResult, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			result = SubscriptionResult{}
			err = E.New("subscription parser panic: ", fmt.Sprint(recovered))
		}
	}()
	return parser(ctx, content)
}

func ParseSubscriptionDetailed(ctx context.Context, content string) (SubscriptionResult, error) {
	var (
		parseErr error
		skipped  []SkippedOutbound
	)
	for _, parser := range subscriptionParsers {
		result, err := callSubscriptionParser(parser, ctx, content)
		if err == nil && len(result.Outbounds) > 0 {
			result = filterUnsupportedOutbounds(ctx, result)
			if len(result.Outbounds) > 0 {
				return result, nil
			}
			err = E.New("provider contains no supported outbounds")
		}
		if len(result.Skipped) > 0 {
			skipped = append(skipped, result.Skipped...)
		}
		parseErr = E.Errors(parseErr, err)
	}
	if parseErr == nil {
		parseErr = E.New("no subscription parser accepted the content")
	}
	if len(skipped) > 0 {
		return SubscriptionResult{Skipped: skipped}, E.Cause(parseErr, "provider contains no supported outbounds")
	}
	return SubscriptionResult{}, E.Cause(parseErr, "no servers found")
}

func filterUnsupportedOutbounds(ctx context.Context, result SubscriptionResult) SubscriptionResult {
	registry := service.FromContext[option.OutboundOptionsRegistry](ctx)
	filtered := make([]option.Outbound, 0, len(result.Outbounds))
	for _, outbound := range result.Outbounds {
		var reason string
		switch outbound.Type {
		case C.TypeDirect, C.TypeBlock, C.TypeDNS, C.TypeSelector, C.TypeURLTest:
			reason = "provider-forbidden outbound type"
		default:
			if registry != nil {
				if _, supported := registry.CreateOptions(outbound.Type); !supported {
					reason = "unsupported by current sing-box version"
				}
			}
		}
		if reason != "" {
			result.Skipped = append(result.Skipped, SkippedOutbound{Tag: outbound.Tag, Type: outbound.Type, Reason: reason})
			continue
		}
		filtered = append(filtered, outbound)
	}
	result.Outbounds = filtered
	return result
}

func ParseSubscription(ctx context.Context, content string) ([]option.Outbound, error) {
	result, err := ParseSubscriptionDetailed(ctx, content)
	return result.Outbounds, err
}

func ValidateProviderOutbounds(outbounds []option.Outbound) error {
	if len(outbounds) == 0 {
		return E.New("provider contains no supported outbounds")
	}
	for index, outbound := range outbounds {
		if outbound.Type == "" || outbound.Options == nil {
			return E.New("invalid provider outbound[", index, "]")
		}
		switch outbound.Type {
		case C.TypeDirect, C.TypeBlock, C.TypeDNS, C.TypeSelector, C.TypeURLTest:
			return E.New("unsupported provider outbound type: ", outbound.Type)
		}
	}
	return nil
}

func ValidateSkippedDependencies(outbounds []option.Outbound, skipped []SkippedOutbound) error {
	if len(skipped) == 0 {
		return nil
	}
	skippedTags := make(map[string]SkippedOutbound, len(skipped))
	for _, outbound := range skipped {
		if outbound.Tag != "" {
			skippedTags[outbound.Tag] = outbound
		}
	}
	for _, outbound := range outbounds {
		wrapper, loaded := outbound.Options.(option.DialerOptionsWrapper)
		if !loaded {
			continue
		}
		detour := wrapper.TakeDialerOptions().Detour
		if skippedOutbound, exists := skippedTags[detour]; exists {
			return E.New("provider outbound ", outbound.Tag, " depends on skipped outbound ", detour, " (", skippedOutbound.Type, ")")
		}
	}
	return nil
}
