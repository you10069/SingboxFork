package local

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/sagernet/fswatch"
	"github.com/sagernet/sing-box/adapter"
	adapterProvider "github.com/sagernet/sing-box/adapter/provider"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing-box/provider/parser"
	"github.com/sagernet/sing/common"
	E "github.com/sagernet/sing/common/exceptions"
	F "github.com/sagernet/sing/common/format"
	"github.com/sagernet/sing/service"
	"github.com/sagernet/sing/service/filemanager"
)

func RegisterProviderLocal(registry *adapterProvider.Registry) {
	adapterProvider.Register[option.ProviderLocalOptions](registry, C.ProviderTypeLocal, NewProviderLocal)
}

func RegisterProviderInline(registry *adapterProvider.Registry) {
	adapterProvider.Register[option.ProviderInlineOptions](registry, C.ProviderTypeInline, NewProviderInline)
}

var _ adapter.Provider = (*ProviderLocal)(nil)

type ProviderLocal struct {
	adapterProvider.Adapter
	ctx     context.Context
	logger  log.ContextLogger
	path    string
	watcher *fswatch.Watcher

	reloadAccess sync.Mutex
	closed       bool
	stateAccess  sync.RWMutex
	lastOutOpts  []option.Outbound
	lastUpdated  time.Time
}

func NewProviderInline(ctx context.Context, router adapter.Router, logFactory log.Factory, tag string, options option.ProviderInlineOptions) (adapter.Provider, error) {
	outboundManager := service.FromContext[adapter.OutboundManager](ctx)
	if outboundManager == nil {
		return nil, E.New("missing outbound manager")
	}
	logger := logFactory.NewLogger(F.ToString("provider/inline[", tag, "]"))
	result := &ProviderLocal{
		Adapter: adapterProvider.NewAdapter(ctx, router, outboundManager, logFactory, logger, tag, C.ProviderTypeInline, options.HealthCheck, options.AdditionalPrefix, options.AdditionalSuffix),
		ctx:     ctx,
		logger:  logger,
	}
	outboundOptions := append([]option.Outbound(nil), options.Outbounds...)
	if err := parser.ValidateProviderOutbounds(outboundOptions); err != nil {
		return nil, err
	}
	result.NormalizeProviderTags(outboundOptions)
	update, err := result.PrepareUpdateOutbounds(outboundOptions)
	if err != nil {
		return nil, err
	}
	if _, err = update.Commit(nil); err != nil {
		return nil, err
	}
	result.stateAccess.Lock()
	result.lastOutOpts = outboundOptions
	result.lastUpdated = time.Now()
	result.stateAccess.Unlock()
	return result, nil
}

func NewProviderLocal(ctx context.Context, router adapter.Router, logFactory log.Factory, tag string, options option.ProviderLocalOptions) (adapter.Provider, error) {
	if options.Path == "" {
		return nil, E.New("provider path is required")
	}
	outboundManager := service.FromContext[adapter.OutboundManager](ctx)
	if outboundManager == nil {
		return nil, E.New("missing outbound manager")
	}
	logger := logFactory.NewLogger(F.ToString("provider/local[", tag, "]"))
	result := &ProviderLocal{
		Adapter: adapterProvider.NewAdapter(ctx, router, outboundManager, logFactory, logger, tag, C.ProviderTypeLocal, options.HealthCheck, options.AdditionalPrefix, options.AdditionalSuffix),
		ctx:     ctx,
		logger:  logger,
	}
	filePath := filemanager.BasePath(ctx, options.Path)
	result.path, _ = filepath.Abs(filePath)
	watcher, err := fswatch.NewWatcher(fswatch.Options{
		Path: []string{filePath},
		Callback: func(path string) {
			if err := result.reloadFile(path); err != nil {
				logger.Error(E.Cause(err, "reload provider ", tag))
			}
		},
	})
	if err != nil {
		return nil, err
	}
	result.watcher = watcher
	return result, nil
}

func (s *ProviderLocal) Start() error {
	if s.Type() == C.ProviderTypeLocal {
		if err := s.reloadFile(s.path); err != nil {
			return err
		}
		if s.watcher != nil {
			if err := s.watcher.Start(); err != nil {
				return E.Cause(err, "watch provider file")
			}
		}
	}
	return s.Adapter.Start()
}

func (s *ProviderLocal) UpdatedAt() time.Time {
	s.stateAccess.RLock()
	defer s.stateAccess.RUnlock()
	return s.lastUpdated
}

func (s *ProviderLocal) reloadFile(path string) error {
	s.reloadAccess.Lock()
	defer s.reloadAccess.Unlock()
	if s.closed {
		return nil
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	result, err := parser.ParseSubscriptionDetailed(s.ctx, string(content))
	if err != nil {
		return err
	}
	for _, skipped := range result.Skipped {
		s.logger.Warn("skip provider outbound ", skipped.Tag, " (", skipped.Type, "): ", skipped.Reason)
	}
	outboundOptions := result.Outbounds
	if err := parser.ValidateSkippedDependencies(outboundOptions, result.Skipped); err != nil {
		return err
	}
	if err := parser.ValidateProviderOutbounds(outboundOptions); err != nil {
		return err
	}
	s.NormalizeProviderTags(outboundOptions)
	update, err := s.PrepareUpdateOutbounds(outboundOptions)
	if err != nil {
		return err
	}
	if _, err = update.Commit(nil); err != nil {
		return err
	}

	updatedAt := time.Now()
	if fileInfo, statErr := os.Stat(path); statErr == nil {
		updatedAt = fileInfo.ModTime()
	}
	s.stateAccess.Lock()
	s.lastOutOpts = append([]option.Outbound(nil), outboundOptions...)
	s.lastUpdated = updatedAt
	s.stateAccess.Unlock()
	return nil
}

func (s *ProviderLocal) Close() error {
	s.reloadAccess.Lock()
	s.closed = true
	s.reloadAccess.Unlock()
	return common.Close(common.PtrOrNil(s.watcher), &s.Adapter)
}
