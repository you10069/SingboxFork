package parser

import (
	"reflect"
	"strconv"

	"github.com/sagernet/sing-box/option"
	F "github.com/sagernet/sing/common/format"
)

// NormalizeProviderTags assigns stable tags to unnamed nodes and makes duplicate
// tags unique. The first occurrence keeps its original name.
func NormalizeProviderTags(outbounds []option.Outbound) {
	used := make(map[string]int, len(outbounds))
	for index := range outbounds {
		base := outbounds[index].Tag
		if base == "" {
			base = strconv.Itoa(index)
		}
		count := used[base]
		used[base] = count + 1
		if count == 0 {
			outbounds[index].Tag = base
			continue
		}
		for suffix := count + 1; ; suffix++ {
			candidate := F.ToString(base, "-", suffix)
			if used[candidate] == 0 {
				outbounds[index].Tag = candidate
				used[candidate] = 1
				break
			}
		}
	}
}

// PrefixProviderDetours rewrites detours that reference another outbound from
// the same provider. Global detours remain unchanged.
func PrefixProviderDetours(providerTag string, outbounds []option.Outbound) {
	localTags := make(map[string]struct{}, len(outbounds))
	for _, outbound := range outbounds {
		if outbound.Tag != "" {
			localTags[outbound.Tag] = struct{}{}
		}
	}
	for index := range outbounds {
		prefixDialerDetour(providerTag, localTags, outbounds[index].Options)
	}
}

func prefixDialerDetour(providerTag string, localTags map[string]struct{}, options any) {
	value := reflect.ValueOf(options)
	if !value.IsValid() || value.Kind() != reflect.Ptr || value.IsNil() {
		return
	}
	value = value.Elem()
	if value.Kind() != reflect.Struct {
		return
	}
	dialerOptions := value.FieldByName("DialerOptions")
	if !dialerOptions.IsValid() || dialerOptions.Kind() != reflect.Struct {
		return
	}
	detour := dialerOptions.FieldByName("Detour")
	if !detour.IsValid() || detour.Kind() != reflect.String || !detour.CanSet() {
		return
	}
	tag := detour.String()
	if tag == "" {
		return
	}
	if _, exists := localTags[tag]; exists {
		detour.SetString(F.ToString(providerTag, "/", tag))
	}
}
