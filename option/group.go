package option

import "github.com/sagernet/sing/common/json/badoption"

type GroupCommonOptions struct {
	Outbounds           []string          `json:"outbounds,omitempty"`
	Providers           []string          `json:"providers,omitempty"`
	Exclude             *badoption.Regexp `json:"exclude,omitempty"`
	Include             *badoption.Regexp `json:"include,omitempty"`
	ExcludeType         *badoption.Regexp `json:"exclude_type,omitempty"`
	IncludeAll          bool              `json:"include_all,omitempty"`
	IncludeAllOutbounds bool              `json:"include_all_outbounds,omitempty"`
	UseAllProviders     bool              `json:"use_all_providers,omitempty"`
	ExcludeAll          bool              `json:"exclude_all,omitempty"`
	ExcludeTypeAll      bool              `json:"exclude_type_all,omitempty"`
}

type SelectorOutboundOptions struct {
	GroupCommonOptions
	Default                   string `json:"default,omitempty"`
	InterruptExistConnections bool   `json:"interrupt_exist_connections,omitempty"`
}

type URLTestOutboundOptions struct {
	GroupCommonOptions
	URL                       string             `json:"url,omitempty"`
	Interval                  badoption.Duration `json:"interval,omitempty"`
	Tolerance                 uint16             `json:"tolerance,omitempty"`
	IdleTimeout               badoption.Duration `json:"idle_timeout,omitempty"`
	InterruptExistConnections bool               `json:"interrupt_exist_connections,omitempty"`
}
