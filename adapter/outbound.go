package adapter

import (
	"context"

	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	N "github.com/sagernet/sing/common/network"
)

// Note: for proxy protocols, outbound creates early connections by default.

type Outbound interface {
	Type() string
	Tag() string
	Network() []string
	Dependencies() []string
	N.Dialer
}

// StaticOutboundMetadata contains ordinary outbounds declared directly in the
// main configuration. Group and DNS outbounds are intentionally excluded so
// automatic collection cannot create self references, group cycles, or select
// the special DNS outbound as a traffic proxy.
type StaticOutboundMetadata struct {
	Tags []string
}

type OutboundRegistry interface {
	option.OutboundOptionsRegistry
	CreateOutbound(ctx context.Context, router Router, logger log.ContextLogger, tag string, outboundType string, options any) (Outbound, error)
}

type OutboundManager interface {
	Lifecycle
	Outbounds() []Outbound
	Outbound(tag string) (Outbound, bool)
	Default() Outbound
	Remove(tag string) error
	Create(ctx context.Context, router Router, logger log.ContextLogger, tag string, outboundType string, options any) error
}

// OutboundBatchItem describes one outbound that is built in an isolated staging
// view before it is published to the live OutboundManager.
type OutboundBatchItem struct {
	Context context.Context
	Router  Router
	Logger  log.ContextLogger
	Tag     string
	Type    string
	Options any
}

type OutboundTransaction interface {
	Outbounds() []Outbound
	Commit(beforePublish func() error) ([]Outbound, error)
	Abort() error
}

// OutboundTransactionManager is an optional extension implemented by the
// built-in outbound manager. It builds a complete batch without touching live
// state, then publishes it atomically when Commit is called.
type OutboundTransactionManager interface {
	PrepareOutbounds(replaceTags []string, items []OutboundBatchItem) (OutboundTransaction, error)
}
