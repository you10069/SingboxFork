package parser

import (
	"context"

	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"
	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/common/json"
	"github.com/sagernet/sing/common/json/badjson"
	"github.com/sagernet/sing/service"
)

type _SingBoxDocument struct {
	Outbounds []option.Outbound `json:"outbounds"`
}
type SingBoxDocument _SingBoxDocument

func filterSingBoxDocument(ctx context.Context, inputContent []byte) ([]byte, []SkippedOutbound, error) {
	var content badjson.JSONObject
	if err := content.UnmarshalJSONContext(ctx, inputContent); err != nil {
		return nil, nil, err
	}
	outboundsValue, loaded := content.Get("outbounds")
	if !loaded {
		return nil, nil, E.New("missing outbounds in sing-box configuration")
	}
	outbounds, ok := outboundsValue.(badjson.JSONArray)
	if !ok {
		return nil, nil, E.New("invalid outbounds in sing-box configuration: expected array")
	}
	registry := service.FromContext[option.OutboundOptionsRegistry](ctx)
	if registry == nil {
		return nil, nil, E.New("missing outbound options registry in context")
	}
	filtered := make(badjson.JSONArray, 0, len(outbounds))
	skipped := make([]SkippedOutbound, 0)
	for index, outboundValue := range outbounds {
		outbound, ok := outboundValue.(*badjson.JSONObject)
		if !ok || outbound == nil {
			return nil, nil, E.New("invalid outbound[", index, "]: expected object")
		}
		typeValue, loaded := outbound.Get("type")
		if !loaded {
			return nil, nil, E.New("missing type in outbound[", index, "]")
		}
		outboundType, ok := typeValue.(string)
		if !ok || outboundType == "" {
			return nil, nil, E.New("invalid type in outbound[", index, "]: expected non-empty string")
		}
		var tag string
		if tagValue, loaded := outbound.Get("tag"); loaded {
			if tagValue != nil {
				var tagOK bool
				tag, tagOK = tagValue.(string)
				if !tagOK {
					return nil, nil, E.New("invalid tag in outbound[", index, "]: expected string")
				}
			}
		}
		switch outboundType {
		case C.TypeDirect, C.TypeBlock, C.TypeDNS, C.TypeSelector, C.TypeURLTest:
			skipped = append(skipped, SkippedOutbound{Tag: tag, Type: outboundType, Reason: "provider-forbidden outbound type"})
			continue
		}
		if _, supported := registry.CreateOptions(outboundType); !supported {
			skipped = append(skipped, SkippedOutbound{Tag: tag, Type: outboundType, Reason: "unsupported by current sing-box version"})
			continue
		}
		filtered = append(filtered, outbound)
	}
	content.Put("outbounds", filtered)
	filteredContent, err := content.MarshalJSONContext(ctx)
	if err != nil {
		return nil, nil, err
	}
	return filteredContent, skipped, nil
}

func (o *SingBoxDocument) UnmarshalJSONContext(ctx context.Context, inputContent []byte) error {
	filteredContent, _, err := filterSingBoxDocument(ctx, inputContent)
	if err != nil {
		return err
	}
	return json.UnmarshalContext(ctx, filteredContent, (*_SingBoxDocument)(o))
}

func ParseBoxSubscriptionDetailed(ctx context.Context, content string) (SubscriptionResult, error) {
	filteredContent, skipped, err := filterSingBoxDocument(ctx, []byte(content))
	if err != nil {
		return SubscriptionResult{}, err
	}
	options, err := json.UnmarshalExtendedContext[_SingBoxDocument](ctx, filteredContent)
	if err != nil {
		return SubscriptionResult{Skipped: skipped}, err
	}
	if len(options.Outbounds) == 0 {
		return SubscriptionResult{Skipped: skipped}, E.New("no supported servers found in sing-box configuration")
	}
	return SubscriptionResult{Outbounds: options.Outbounds, Skipped: skipped}, nil
}

func ParseBoxSubscription(ctx context.Context, content string) ([]option.Outbound, error) {
	result, err := ParseBoxSubscriptionDetailed(ctx, content)
	return result.Outbounds, err
}
