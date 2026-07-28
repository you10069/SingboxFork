package option

import "github.com/sagernet/sing/common/json/badoption"

type SelectorOutboundOptions struct {
	Outbounds                 []string          `json:"outbounds"`
	IncludeAllOutbounds       bool              `json:"include_all_outbounds,omitempty"`
	Include                   *badoption.Regexp `json:"include,omitempty"`
	Exclude                   *badoption.Regexp `json:"exclude,omitempty"`
	ExcludeType               *badoption.Regexp `json:"exclude_type,omitempty"`
	Default                   string            `json:"default,omitempty"`
	InterruptExistConnections bool              `json:"interrupt_exist_connections,omitempty"`
}

type URLTestOutboundOptions struct {
	Outbounds                 []string           `json:"outbounds"`
	URL                       string             `json:"url,omitempty"`
	Interval                  badoption.Duration `json:"interval,omitempty"`
	Tolerance                 uint16             `json:"tolerance,omitempty"`
	IdleTimeout               badoption.Duration `json:"idle_timeout,omitempty"`
	InterruptExistConnections bool               `json:"interrupt_exist_connections,omitempty"`
}
