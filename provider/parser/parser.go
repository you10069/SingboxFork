package parser

import (
	"context"

	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"
	E "github.com/sagernet/sing/common/exceptions"
)

var subscriptionParsers = []func(ctx context.Context, content string) ([]option.Outbound, error){
	ParseBoxSubscription,
	ParseClashSubscription,
	ParseSIP008Subscription,
	ParseRawSubscription,
}

func ParseSubscription(ctx context.Context, content string) ([]option.Outbound, error) {
	var pErr error
	for _, parser := range subscriptionParsers {
		servers, err := parser(ctx, content)
		if len(servers) > 0 {
			return servers, nil
		}
		pErr = E.Errors(pErr, err)
	}
	return nil, E.Cause(pErr, "no servers found")
}

func ValidateProviderOutbounds(outbounds []option.Outbound) error {
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
