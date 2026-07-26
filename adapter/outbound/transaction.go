package outbound

import (
	"context"
	"os"
	"strings"
	"sync"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing/common"
	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/service"
)

var _ adapter.OutboundTransactionManager = (*Manager)(nil)

type overlayRegistry struct {
	base   service.Registry
	access sync.RWMutex
	values map[any]any
}

func newOutboundManagerContext(ctx context.Context, manager adapter.OutboundManager) context.Context {
	registry := &overlayRegistry{
		base:   service.RegistryFromContext(ctx),
		values: make(map[any]any),
	}
	registry.values[common.DefaultValue[*adapter.OutboundManager]()] = manager
	return service.ContextWithRegistry(ctx, registry)
}

func (r *overlayRegistry) Register(serviceType any, value any) any {
	r.access.Lock()
	defer r.access.Unlock()
	oldValue := r.values[serviceType]
	if oldValue == nil && r.base != nil {
		oldValue = r.base.Get(serviceType)
	}
	r.values[serviceType] = value
	return oldValue
}

func (r *overlayRegistry) Get(serviceType any) any {
	r.access.RLock()
	value, loaded := r.values[serviceType]
	r.access.RUnlock()
	if loaded {
		return value
	}
	if r.base == nil {
		return nil
	}
	return r.base.Get(serviceType)
}

type stagingManager struct {
	base       *Manager
	access     sync.RWMutex
	candidates map[string]adapter.Outbound
	committed  bool
}

func newStagingManager(base *Manager) *stagingManager {
	return &stagingManager{base: base, candidates: make(map[string]adapter.Outbound)}
}

func (m *stagingManager) Start(adapter.StartStage) error { return nil }
func (m *stagingManager) Close() error                   { return nil }
func (m *stagingManager) Default() adapter.Outbound      { return m.base.Default() }
func (m *stagingManager) Remove(string) error            { return os.ErrPermission }
func (m *stagingManager) Create(context.Context, adapter.Router, log.ContextLogger, string, string, any) error {
	return os.ErrPermission
}

func (m *stagingManager) Outbounds() []adapter.Outbound {
	m.access.RLock()
	if !m.committed {
		result := make([]adapter.Outbound, 0, len(m.candidates))
		for _, outbound := range m.candidates {
			result = append(result, outbound)
		}
		m.access.RUnlock()
		return result
	}
	m.access.RUnlock()
	return m.base.Outbounds()
}

func (m *stagingManager) Outbound(tag string) (adapter.Outbound, bool) {
	m.access.RLock()
	if !m.committed {
		if outbound, loaded := m.candidates[tag]; loaded {
			m.access.RUnlock()
			return outbound, true
		}
	}
	m.access.RUnlock()
	return m.base.Outbound(tag)
}

func (m *stagingManager) add(outbound adapter.Outbound) {
	m.access.Lock()
	m.candidates[outbound.Tag()] = outbound
	m.access.Unlock()
}

func (m *stagingManager) commit() {
	m.access.Lock()
	m.committed = true
	m.candidates = nil
	m.access.Unlock()
}

func startStagedOutboundBatch(outbounds []adapter.Outbound, candidateTags map[string]struct{}) error {
	started := make(map[string]bool, len(outbounds))
	for len(started) < len(outbounds) {
		progressed := false
		for _, outbound := range outbounds {
			if started[outbound.Tag()] {
				continue
			}
			ready := true
			for _, dependency := range outbound.Dependencies() {
				if _, local := candidateTags[dependency]; local && !started[dependency] {
					ready = false
					break
				}
			}
			if !ready {
				continue
			}
			if err := adapter.LegacyStart(outbound, adapter.StartStateStart); err != nil {
				return E.Cause(err, "start staged outbound/", outbound.Type(), "[", outbound.Tag(), "]")
			}
			started[outbound.Tag()] = true
			progressed = true
		}
		if progressed {
			continue
		}
		pending := make([]string, 0, len(outbounds)-len(started))
		for _, outbound := range outbounds {
			if !started[outbound.Tag()] {
				pending = append(pending, outbound.Tag())
			}
		}
		return E.New("circular staged outbound dependency: ", strings.Join(pending, ", "))
	}
	return nil
}

func closeOutboundBatch(outbounds []adapter.Outbound) error {
	var result error
	for index := len(outbounds) - 1; index >= 0; index-- {
		outbound := outbounds[index]
		result = E.Append(result, common.Close(outbound), func(err error) error {
			return E.Cause(err, "close staged outbound/", outbound.Type(), "[", outbound.Tag(), "]")
		})
	}
	return result
}

type outboundTransaction struct {
	manager       *Manager
	staging       *stagingManager
	replaceTagSet map[string]struct{}
	newTagSet     map[string]struct{}
	oldSnapshot   map[string]adapter.Outbound
	created       []adapter.Outbound
	started       bool
	stage         adapter.StartStage
	access        sync.Mutex
	finished      bool
	lockReleased  bool
}

func (t *outboundTransaction) releaseManagerTransactionLock() {
	if t.lockReleased {
		return
	}
	t.lockReleased = true
	t.manager.transactionAccess.Unlock()
}

func (t *outboundTransaction) Outbounds() []adapter.Outbound {
	t.access.Lock()
	defer t.access.Unlock()
	return append([]adapter.Outbound(nil), t.created...)
}

func (t *outboundTransaction) Abort() error {
	t.access.Lock()
	if t.finished {
		t.access.Unlock()
		return nil
	}
	t.finished = true
	created := append([]adapter.Outbound(nil), t.created...)
	err := closeOutboundBatch(created)
	t.releaseManagerTransactionLock()
	t.access.Unlock()
	return err
}

func (t *outboundTransaction) Commit(beforePublish func() error) ([]adapter.Outbound, error) {
	t.access.Lock()
	if t.finished {
		t.access.Unlock()
		return nil, os.ErrClosed
	}
	m := t.manager
	endpointDependencies := make(map[string]bool)
	for _, outbound := range t.created {
		for _, dependency := range outbound.Dependencies() {
			if _, local := t.newTagSet[dependency]; local {
				continue
			}
			if _, removing := t.replaceTagSet[dependency]; removing {
				continue
			}
			_, endpointDependencies[dependency] = m.endpoint.Get(dependency)
		}
	}
	m.access.Lock()
	if m.started != t.started || m.stage != t.stage {
		m.access.Unlock()
		t.access.Unlock()
		_ = t.Abort()
		return nil, E.New("outbound manager lifecycle changed during transaction")
	}
	for tag := range t.replaceTagSet {
		oldOutbound, existed := t.oldSnapshot[tag]
		current, loaded := m.outboundByTag[tag]
		if (existed && (!loaded || current != oldOutbound)) || (!existed && loaded) {
			m.access.Unlock()
			t.access.Unlock()
			_ = t.Abort()
			return nil, E.New("outbound changed during transaction: ", tag)
		}
	}
	for tag := range t.newTagSet {
		if _, owned := t.replaceTagSet[tag]; owned {
			continue
		}
		if _, loaded := m.outboundByTag[tag]; loaded {
			m.access.Unlock()
			t.access.Unlock()
			_ = t.Abort()
			return nil, E.New("outbound tag conflicts with existing outbound: ", tag)
		}
	}
	for tag := range t.replaceTagSet {
		if _, retained := t.newTagSet[tag]; retained {
			continue
		}
		for _, dependent := range m.dependByTag[tag] {
			if _, replacedTogether := t.replaceTagSet[dependent]; !replacedTogether {
				m.access.Unlock()
				t.access.Unlock()
				_ = t.Abort()
				return nil, E.New("outbound[", tag, "] is depended by ", dependent)
			}
		}
	}
	for _, outbound := range t.created {
		for _, dependency := range outbound.Dependencies() {
			if _, local := t.newTagSet[dependency]; local {
				continue
			}
			if _, removing := t.replaceTagSet[dependency]; removing {
				m.access.Unlock()
				t.access.Unlock()
				_ = t.Abort()
				return nil, E.New("dependency[", dependency, "] is removed for outbound[", outbound.Tag(), "]")
			}
			if _, loaded := m.outboundByTag[dependency]; loaded {
				continue
			}
			if endpointDependencies[dependency] {
				continue
			}
			m.access.Unlock()
			t.access.Unlock()
			_ = t.Abort()
			return nil, E.New("dependency[", dependency, "] not found for outbound[", outbound.Tag(), "]")
		}
	}
	if beforePublish != nil {
		if err := beforePublish(); err != nil {
			m.access.Unlock()
			t.access.Unlock()
			_ = t.Abort()
			return nil, err
		}
	}

	replaced := make([]adapter.Outbound, 0, len(t.oldSnapshot))
	firstOldIndex := -1
	remaining := make([]adapter.Outbound, 0, len(m.outbounds)-len(t.oldSnapshot)+len(t.created))
	for _, outbound := range m.outbounds {
		if _, removing := t.replaceTagSet[outbound.Tag()]; removing {
			if firstOldIndex == -1 {
				firstOldIndex = len(remaining)
			}
			replaced = append(replaced, outbound)
			m.removeDependencyReferencesLocked(outbound.Tag(), outbound.Dependencies())
			delete(m.outboundByTag, outbound.Tag())
			if _, retained := t.newTagSet[outbound.Tag()]; !retained {
				delete(m.dependByTag, outbound.Tag())
			}
			continue
		}
		remaining = append(remaining, outbound)
	}
	if firstOldIndex == -1 {
		firstOldIndex = len(remaining)
	}
	withCreated := make([]adapter.Outbound, 0, len(remaining)+len(t.created))
	withCreated = append(withCreated, remaining[:firstOldIndex]...)
	withCreated = append(withCreated, t.created...)
	withCreated = append(withCreated, remaining[firstOldIndex:]...)
	m.outbounds = withCreated
	for _, outbound := range t.created {
		m.outboundByTag[outbound.Tag()] = outbound
		for _, dependency := range outbound.Dependencies() {
			if !common.Contains(m.dependByTag[dependency], outbound.Tag()) {
				m.dependByTag[dependency] = append(m.dependByTag[dependency], outbound.Tag())
			}
		}
	}
	if m.defaultOutbound != nil {
		if _, replacedDefault := t.replaceTagSet[m.defaultOutbound.Tag()]; replacedDefault {
			m.defaultOutbound = m.outboundByTag[m.defaultOutbound.Tag()]
		}
	}
	if m.defaultOutbound == nil {
		if m.defaultTag != "" {
			m.defaultOutbound = m.outboundByTag[m.defaultTag]
		}
		if m.defaultOutbound == nil && len(m.outbounds) > 0 {
			m.defaultOutbound = m.outbounds[0]
		}
	}
	t.staging.commit()
	t.finished = true
	m.access.Unlock()
	t.releaseManagerTransactionLock()
	t.access.Unlock()

	return replaced, nil
}

func (m *Manager) PrepareOutbounds(replaceTags []string, items []adapter.OutboundBatchItem) (adapter.OutboundTransaction, error) {
	m.transactionAccess.Lock()
	unlockOnError := true
	defer func() {
		if unlockOnError {
			m.transactionAccess.Unlock()
		}
	}()
	newTagSet := make(map[string]struct{}, len(items))
	for _, item := range items {
		if item.Tag == "" {
			return nil, os.ErrInvalid
		}
		if _, exists := newTagSet[item.Tag]; exists {
			return nil, E.New("duplicate outbound tag in transaction: ", item.Tag)
		}
		newTagSet[item.Tag] = struct{}{}
	}
	replaceTagSet := make(map[string]struct{}, len(replaceTags))
	for _, tag := range replaceTags {
		replaceTagSet[tag] = struct{}{}
	}

	for tag := range newTagSet {
		if _, loaded := m.endpoint.Get(tag); loaded {
			return nil, E.New("outbound tag conflicts with existing endpoint: ", tag)
		}
	}
	m.access.Lock()
	started := m.started
	stage := m.stage
	oldSnapshot := make(map[string]adapter.Outbound, len(replaceTagSet))
	for tag := range replaceTagSet {
		if outbound, loaded := m.outboundByTag[tag]; loaded {
			oldSnapshot[tag] = outbound
		}
	}
	for tag := range newTagSet {
		if _, owned := replaceTagSet[tag]; owned {
			continue
		}
		if _, loaded := m.outboundByTag[tag]; loaded {
			m.access.Unlock()
			return nil, E.New("outbound tag conflicts with existing outbound: ", tag)
		}
	}
	m.access.Unlock()

	staging := newStagingManager(m)
	created := make([]adapter.Outbound, 0, len(items))
	for _, item := range items {
		itemContext := item.Context
		if itemContext == nil {
			itemContext = context.Background()
		}
		itemContext = newOutboundManagerContext(itemContext, staging)
		outbound, err := m.registry.CreateOutbound(itemContext, item.Router, item.Logger, item.Tag, item.Type, item.Options)
		if err != nil {
			_ = closeOutboundBatch(created)
			return nil, E.Cause(err, "create staged outbound/", item.Type, "[", item.Tag, "]")
		}
		staging.add(outbound)
		created = append(created, outbound)
	}
	if started {
		var startErr error
		for _, startStage := range adapter.ListStartStages {
			if startStage > stage {
				break
			}
			if startStage == adapter.StartStateStart {
				startErr = startStagedOutboundBatch(created, newTagSet)
			} else {
				for _, outbound := range created {
					if startErr = adapter.LegacyStart(outbound, startStage); startErr != nil {
						startErr = E.Cause(startErr, startStage, " staged outbound/", outbound.Type(), "[", outbound.Tag(), "]")
						break
					}
				}
			}
			if startErr != nil {
				_ = closeOutboundBatch(created)
				return nil, startErr
			}
		}
	}
	unlockOnError = false
	return &outboundTransaction{
		manager:       m,
		staging:       staging,
		replaceTagSet: replaceTagSet,
		newTagSet:     newTagSet,
		oldSnapshot:   oldSnapshot,
		created:       created,
		started:       started,
		stage:         stage,
	}, nil
}
