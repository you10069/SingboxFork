package parser

import (
	"reflect"
	"strconv"

	"github.com/sagernet/sing-box/option"
	F "github.com/sagernet/sing/common/format"
)

// NormalizeProviderTags applies user-provided affixes, assigns stable tags to
// unnamed nodes, and makes duplicate tags unique. Affixes are concatenated
// verbatim and the first occurrence keeps the resulting name.
func NormalizeProviderTags(outbounds []option.Outbound, additionalPrefix string, additionalSuffix string) {
	originalTags := make([]string, len(outbounds))
	used := make(map[string]int, len(outbounds))
	for index := range outbounds {
		originalTags[index] = outbounds[index].Tag
		base := outbounds[index].Tag
		if base == "" {
			base = strconv.Itoa(index)
		}
		base = F.ToString(additionalPrefix, base, additionalSuffix)
		count := used[base]
		used[base] = count + 1
		if count == 0 {
			outbounds[index].Tag = base
			continue
		}
		for suffix := count + 1; ; suffix++ {
			candidate := F.ToString(base, " (", suffix, ")")
			if used[candidate] == 0 {
				outbounds[index].Tag = candidate
				used[candidate] = 1
				break
			}
		}
	}
	renamedTags := make(map[string]string, len(outbounds))
	for index, originalTag := range originalTags {
		if originalTag == "" {
			continue
		}
		if _, exists := renamedTags[originalTag]; !exists {
			renamedTags[originalTag] = outbounds[index].Tag
		}
	}
	for index := range outbounds {
		rewriteDialerDetour(renamedTags, outbounds[index].Options)
	}
}

// PrefixProviderDetours rewrites detours that reference another outbound from
// the same provider. Global detours remain unchanged.
func PrefixProviderDetours(providerTag string, additionalPrefix string, outbounds []option.Outbound) {
	if additionalPrefix != "" {
		return
	}
	localTags := make(map[string]string, len(outbounds))
	for _, outbound := range outbounds {
		if outbound.Tag != "" {
			localTags[outbound.Tag] = F.ToString(providerTag, "_", outbound.Tag)
		}
	}
	for index := range outbounds {
		rewriteDialerDetour(localTags, outbounds[index].Options)
	}
}

func rewriteDialerDetour(replacements map[string]string, options any) {
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
	if replacement, exists := replacements[tag]; exists {
		detour.SetString(replacement)
	}
}
