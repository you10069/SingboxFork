package group

import (
	"regexp"

	"github.com/sagernet/sing-box/adapter"
	E "github.com/sagernet/sing/common/exceptions"
)

type groupFilterOptions struct {
	include        *regexp.Regexp
	exclude        *regexp.Regexp
	excludeType    *regexp.Regexp
	excludeAll     bool
	excludeTypeAll bool
}

type groupCandidate struct {
	tag      string
	outbound adapter.Outbound
	explicit bool
}

func collectGroupOutbounds(
	manager adapter.OutboundManager,
	groupTag string,
	explicitTags []string,
	automaticStaticTags []string,
	providerTags []string,
	providers map[string]adapter.Provider,
	providerOverrides map[string][]adapter.Outbound,
	filters groupFilterOptions,
) ([]adapter.Outbound, []string, error) {
	candidates := make([]groupCandidate, 0)
	candidateByTag := make(map[string]int)
	appendCandidate := func(tag string, outbound adapter.Outbound, explicit bool) {
		if tag == "" || outbound == nil || tag == groupTag {
			return
		}
		if index, exists := candidateByTag[tag]; exists {
			// Explicit declarations take precedence over automatically collected
			// copies of the same tag for include/exclude scope decisions.
			if explicit && !candidates[index].explicit {
				candidates[index].explicit = true
			}
			return
		}
		candidateByTag[tag] = len(candidates)
		candidates = append(candidates, groupCandidate{tag: tag, outbound: outbound, explicit: explicit})
	}

	for index, tag := range explicitTags {
		outbound, loaded := manager.Outbound(tag)
		if !loaded {
			return nil, nil, E.New("outbound ", index, " not found: ", tag)
		}
		appendCandidate(tag, outbound, true)
	}
	for _, tag := range automaticStaticTags {
		outbound, loaded := manager.Outbound(tag)
		if !loaded {
			return nil, nil, E.New("automatically included outbound not found: ", tag)
		}
		appendCandidate(tag, outbound, false)
	}
	for _, providerTag := range providerTags {
		if override, loaded := providerOverrides[providerTag]; loaded {
			for _, outbound := range override {
				appendCandidate(outbound.Tag(), outbound, false)
			}
			continue
		}
		provider := providers[providerTag]
		if provider == nil {
			continue
		}
		for _, outbound := range provider.Outbounds() {
			appendCandidate(outbound.Tag(), outbound, false)
		}
	}

	outbounds := make([]adapter.Outbound, 0, len(candidates))
	tags := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		if !candidate.explicit && filters.include != nil && !filters.include.MatchString(candidate.tag) {
			continue
		}
		if filters.exclude != nil && (filters.excludeAll || !candidate.explicit) && filters.exclude.MatchString(candidate.tag) {
			continue
		}
		if filters.excludeType != nil && (filters.excludeTypeAll || !candidate.explicit) && filters.excludeType.MatchString(candidate.outbound.Type()) {
			continue
		}
		tags = append(tags, candidate.tag)
		outbounds = append(outbounds, candidate.outbound)
	}
	return outbounds, tags, nil
}
