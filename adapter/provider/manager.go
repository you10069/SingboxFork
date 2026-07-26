package provider

import (
	"context"
	"io"
	"os"
	"sync"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/taskmonitor"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing/common"
	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/common/logger"
)

var _ adapter.ProviderManager = (*Manager)(nil)

type Manager struct {
	logger        log.ContextLogger
	registry      adapter.ProviderRegistry
	access        sync.Mutex
	started       bool
	stage         adapter.StartStage
	providers     []adapter.Provider
	providerByTag map[string]adapter.Provider
}

func NewManager(logger logger.ContextLogger, registry adapter.ProviderRegistry) *Manager {
	return &Manager{
		logger:        logger,
		registry:      registry,
		providerByTag: make(map[string]adapter.Provider),
	}
}

func (m *Manager) Initialize() {
}

func (m *Manager) Start(stage adapter.StartStage) error {
	m.access.Lock()
	if m.started && m.stage >= stage {
		panic("already started")
	}
	m.started = true
	m.stage = stage
	providers := append([]adapter.Provider(nil), m.providers...)
	m.access.Unlock()
	for _, provider := range providers {
		err := adapter.LegacyStart(provider, stage)
		if err != nil {
			return E.Cause(err, stage, " provider/", provider.Type(), "[", provider.Tag(), "]")
		}
	}
	return nil
}

func (m *Manager) Close() error {
	monitor := taskmonitor.New(m.logger, C.StopTimeout)
	m.access.Lock()
	m.started = false
	providers := append([]adapter.Provider(nil), m.providers...)
	m.providers = nil
	m.providerByTag = make(map[string]adapter.Provider)
	m.access.Unlock()
	var err error
	for _, provider := range providers {
		if closer, isCloser := provider.(io.Closer); isCloser {
			monitor.Start("close provider/", provider.Type(), "[", provider.Tag(), "]")
			err = E.Append(err, closer.Close(), func(err error) error {
				return E.Cause(err, "close provider/", provider.Type(), "[", provider.Tag(), "]")
			})
			monitor.Finish()
		}
	}
	return err
}

func (m *Manager) Providers() []adapter.Provider {
	m.access.Lock()
	defer m.access.Unlock()
	return append([]adapter.Provider(nil), m.providers...)
}

func (m *Manager) Get(tag string) (adapter.Provider, bool) {
	m.access.Lock()
	provider, found := m.providerByTag[tag]
	m.access.Unlock()
	return provider, found
}

func (m *Manager) Remove(tag string) error {
	m.access.Lock()
	defer m.access.Unlock()
	provider, found := m.providerByTag[tag]
	if !found {
		return os.ErrInvalid
	}
	if err := common.Close(provider); err != nil {
		return E.Cause(err, "close provider/", provider.Type(), "[", provider.Tag(), "]")
	}
	delete(m.providerByTag, tag)
	index := common.Index(m.providers, func(it adapter.Provider) bool {
		return it == provider
	})
	if index == -1 {
		panic("invalid provider index")
	}
	m.providers = append(m.providers[:index], m.providers[index+1:]...)
	return nil
}

func (m *Manager) Create(ctx context.Context, router adapter.Router, logFactory log.Factory, tag string, providerType string, options any) error {
	if tag == "" {
		return os.ErrInvalid
	}

	m.access.Lock()
	_, exists := m.providerByTag[tag]
	m.access.Unlock()
	if exists {
		return E.New("provider already exists: ", tag)
	}

	provider, err := m.registry.CreateProvider(ctx, router, logFactory, tag, providerType, options)
	if err != nil {
		return err
	}
	m.access.Lock()
	defer m.access.Unlock()
	// Protect against concurrent creation even though configuration startup is
	// normally single-threaded.
	if _, exists = m.providerByTag[tag]; exists {
		_ = common.Close(provider)
		return E.New("provider already exists: ", tag)
	}
	if m.started {
		for _, stage := range adapter.ListStartStages {
			if stage > m.stage {
				break
			}
			err = adapter.LegacyStart(provider, stage)
			if err != nil {
				_ = common.Close(provider)
				return E.Cause(err, stage, " provider/", provider.Type(), "[", provider.Tag(), "]")
			}
		}
	}
	m.providers = append(m.providers, provider)
	m.providerByTag[tag] = provider
	return nil
}
