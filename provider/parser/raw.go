package parser

import (
	"context"
	"encoding/base64"
	"strings"

	"github.com/sagernet/sing-box/option"
	E "github.com/sagernet/sing/common/exceptions"
)

var supportedSubscriptionSchemes = map[string]struct{}{
	"ss": {}, "vmess": {}, "vless": {}, "trojan": {},
	"hysteria": {}, "hy2": {}, "hysteria2": {}, "tuic": {},
}

func ParseRawSubscriptionDetailed(_ context.Context, content string) (SubscriptionResult, error) {
	if base64Content, err := DecodeBase64URLSafe(content); err == nil {
		return parseRawSubscriptionDetailed(base64Content)
	}
	return parseRawSubscriptionDetailed(content)
}

func ParseRawSubscription(ctx context.Context, content string) ([]option.Outbound, error) {
	result, err := ParseRawSubscriptionDetailed(ctx, content)
	return result.Outbounds, err
}

func parseRawSubscriptionDetailed(content string) (SubscriptionResult, error) {
	var result SubscriptionResult
	content = strings.ReplaceAll(content, "\r\n", "\n")
	for lineIndex, rawLine := range strings.Split(content, "\n") {
		linkLine := strings.TrimSpace(rawLine)
		if linkLine == "" || strings.HasPrefix(linkLine, "#") {
			continue
		}
		separator := strings.Index(linkLine, "://")
		if separator <= 0 {
			continue
		}
		scheme := strings.ToLower(linkLine[:separator])
		if _, supported := supportedSubscriptionSchemes[scheme]; !supported {
			result.Skipped = append(result.Skipped, SkippedOutbound{Type: scheme, Reason: "unsupported subscription link scheme"})
			continue
		}
		server, err := ParseSubscriptionLink(linkLine)
		if err != nil {
			return result, E.Cause(err, "parse supported subscription link[", lineIndex, "] (", scheme, ")")
		}
		result.Outbounds = append(result.Outbounds, server)
	}
	if len(result.Outbounds) == 0 {
		if len(result.Skipped) > 0 {
			return result, E.New("provider contains no supported outbounds")
		}
		return result, E.New("no servers found")
	}
	return result, nil
}

func DecodeBase64URLSafe(content string) (string, error) {
	s := strings.ReplaceAll(content, " ", "-")
	s = strings.ReplaceAll(s, "/", "_")
	s = strings.ReplaceAll(s, "+", "-")
	s = strings.ReplaceAll(s, "=", "")
	result, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return content, err
	}
	return string(result), nil
}
