package provider

import (
	"context"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/urltest"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	providerParser "github.com/sagernet/sing-box/provider/parser"
	"github.com/sagernet/sing/common"
	"github.com/sagernet/sing/common/batch"
	E "github.com/sagernet/sing/common/exceptions"
	F "github.com/sagernet/sing/common/format"
	"github.com/sagernet/sing/common/x/list"
	"github.com/sagernet/sing/service"
)

var _ adapter.ProviderUpdatePreparer = (*Adapter)(nil)

type Adapter struct {
	ctx              context.Context
	cancel           context.CancelFunc
	outbound         adapter.OutboundManager
	router           adapter.Router
	logFactory       log.Factory
	logger           log.ContextLogger
	providerType     string
	providerTag      string
	additionalPrefix string
	additionalSuffix string

	updateAccess    sync.Mutex
	outboundsAccess sync.RWMutex
	outbounds       []adapter.Outbound
	outboundsByTag  map[string]adapter.Outbound

	tickerAccess sync.Mutex
	ticker       *time.Ticker
	checking     atomic.Bool
	history      *urltest.HistoryStorage

	callbackAccess        sync.Mutex
	callbacks             list.List[adapter.ProviderUpdateCallback]
	prepareCallbackAccess sync.Mutex
	prepareCallbacks      list.List[adapter.ProviderUpdatePrepareCallback]

	link     string
	enabled  bool
	timeout  time.Duration
	interval time.Duration
}

func NewAdapter(ctx context.Context, router adapter.Router, outbound adapter.OutboundManager, logFactory log.Factory, logger log.ContextLogger, providerTag string, providerType string, options option.ProviderHealthCheckOptions, additionalPrefix string, additionalSuffix string) Adapter {
	ctx, cancel := context.WithCancel(ctx)
	timeout := time.Duration(options.Timeout)
	if timeout == 0 {
		timeout = 3 * time.Second
	}
	interval := time.Duration(options.Interval)
	if interval == 0 {
		interval = 10 * time.Minute
	}
	if interval < time.Minute {
		interval = time.Minute
	}
	return Adapter{
		ctx:              ctx,
		cancel:           cancel,
		outbound:         outbound,
		router:           router,
		logFactory:       logFactory,
		logger:           logger,
		providerType:     providerType,
		providerTag:      providerTag,
		additionalPrefix: additionalPrefix,
		additionalSuffix: additionalSuffix,
		enabled:          options.Enabled,
		link:             options.URL,
		timeout:          timeout,
		interval:         interval,
	}
}

func (a *Adapter) Start() error {
	a.history = service.PtrFromContext[urltest.HistoryStorage](a.ctx)
	if a.history == nil {
		if clashServer := service.FromContext[adapter.ClashServer](a.ctx); clashServer != nil {
			a.history = clashServer.HistoryStorage()
		} else {
			a.history = urltest.NewHistoryStorage()
		}
	}
	if a.enabled {
		go a.loopCheck()
	}
	return nil
}

func (a *Adapter) Type() string { return a.providerType }
func (a *Adapter) Tag() string  { return a.providerTag }

func (a *Adapter) Outbounds() []adapter.Outbound {
	a.outboundsAccess.RLock()
	defer a.outboundsAccess.RUnlock()
	return append([]adapter.Outbound(nil), a.outbounds...)
}

func (a *Adapter) Outbound(tag string) (adapter.Outbound, bool) {
	a.outboundsAccess.RLock()
	defer a.outboundsAccess.RUnlock()
	detour, ok := a.outboundsByTag[tag]
	return detour, ok
}

func (a *Adapter) NormalizeProviderTags(outbounds []option.Outbound) {
	providerParser.NormalizeProviderTags(outbounds, a.additionalPrefix, a.additionalSuffix)
}

func (a *Adapter) providerOutboundTag(optionTag string, index int) string {
	if a.additionalPrefix != "" {
		if optionTag != "" {
			return optionTag
		}
		return F.ToString(a.additionalPrefix, index, a.additionalSuffix)
	}
	if optionTag != "" {
		return F.ToString(a.providerTag, "_", optionTag)
	}
	return F.ToString(a.providerTag, "_", index, a.additionalSuffix)
}

type PreparedOutboundUpdate struct {
	adapter     *Adapter
	options     []option.Outbound
	newTagSet   map[string]struct{}
	transaction adapter.OutboundTransaction
	finished    bool
}

func (a *Adapter) PrepareUpdateOutbounds(newOptions []option.Outbound) (*PreparedOutboundUpdate, error) {
	a.updateAccess.Lock()
	unlockOnError := true
	defer func() {
		if unlockOnError {
			a.updateAccess.Unlock()
		}
	}()

	preparedOptions := cloneProviderOutbounds(newOptions)
	providerParser.PrefixProviderDetours(a.providerTag, a.additionalPrefix, preparedOptions)

	newTagSet := make(map[string]struct{}, len(newOptions))
	for index, outboundOptions := range newOptions {
		tag := a.providerOutboundTag(outboundOptions.Tag, index)
		if _, exists := newTagSet[tag]; exists {
			return nil, E.New("duplicate provider outbound tag: ", tag)
		}
		newTagSet[tag] = struct{}{}
	}

	a.outboundsAccess.RLock()
	currentOutbounds := append([]adapter.Outbound(nil), a.outbounds...)
	ownedTags := make(map[string]struct{}, len(a.outboundsByTag))
	for tag := range a.outboundsByTag {
		ownedTags[tag] = struct{}{}
	}
	a.outboundsAccess.RUnlock()
	for tag := range newTagSet {
		if _, owned := ownedTags[tag]; owned {
			continue
		}
		if _, exists := a.outbound.Outbound(tag); exists {
			return nil, E.New("provider outbound tag conflicts with existing outbound: ", tag)
		}
	}

	creationOrder, _, err := a.providerCreationOrder(preparedOptions)
	if err != nil {
		return nil, err
	}
	transactionManager, loaded := a.outbound.(adapter.OutboundTransactionManager)
	if !loaded {
		return nil, E.New("outbound manager does not support provider transactions")
	}
	replaceTags := make([]string, 0, len(currentOutbounds))
	for _, outbound := range currentOutbounds {
		replaceTags = append(replaceTags, outbound.Tag())
	}
	items := make([]adapter.OutboundBatchItem, 0, len(preparedOptions))
	for _, index := range creationOrder {
		outboundOptions := preparedOptions[index]
		tag := a.providerOutboundTag(newOptions[index].Tag, index)
		items = append(items, adapter.OutboundBatchItem{
			Context: adapter.WithContext(a.ctx, &adapter.InboundContext{Outbound: tag}),
			Router:  a.router,
			Logger:  a.logFactory.NewLogger(F.ToString("outbound/", outboundOptions.Type, "[", tag, "]")),
			Tag:     tag,
			Type:    outboundOptions.Type,
			Options: outboundOptions.Options,
		})
	}
	transaction, err := transactionManager.PrepareOutbounds(replaceTags, items)
	if err != nil {
		return nil, err
	}
	unlockOnError = false
	return &PreparedOutboundUpdate{
		adapter:     a,
		options:     append([]option.Outbound(nil), newOptions...),
		newTagSet:   newTagSet,
		transaction: transaction,
	}, nil
}

func (u *PreparedOutboundUpdate) Abort() error {
	if u == nil || u.finished {
		return nil
	}
	u.finished = true
	err := u.transaction.Abort()
	u.adapter.updateAccess.Unlock()
	return err
}

func (u *PreparedOutboundUpdate) Commit(beforePublish func() error) ([]option.Outbound, error) {
	if u == nil || u.finished {
		return nil, E.New("provider outbound update is already finished")
	}
	u.finished = true
	a := u.adapter
	created := u.transaction.Outbounds()
	outboundByTag := make(map[string]adapter.Outbound, len(created))
	for _, outbound := range created {
		outboundByTag[outbound.Tag()] = outbound
	}
	orderedOutbounds := make([]adapter.Outbound, 0, len(u.options))
	for index, outboundOptions := range u.options {
		tag := a.providerOutboundTag(outboundOptions.Tag, index)
		outbound := outboundByTag[tag]
		if outbound == nil {
			_ = u.transaction.Abort()
			a.updateAccess.Unlock()
			return nil, E.New("provider outbound missing before transaction commit: ", tag)
		}
		orderedOutbounds = append(orderedOutbounds, outbound)
	}
	preparations, err := a.prepareGroupUpdates(orderedOutbounds)
	if err != nil {
		_ = u.transaction.Abort()
		a.updateAccess.Unlock()
		return nil, err
	}
	preparationsCommitted := false
	publish := func() error {
		if beforePublish != nil {
			if err := beforePublish(); err != nil {
				return err
			}
		}
		for _, preparation := range preparations {
			preparation.Commit()
		}
		preparationsCommitted = true
		return nil
	}
	replaced, err := u.transaction.Commit(publish)
	if err != nil {
		if !preparationsCommitted {
			for index := len(preparations) - 1; index >= 0; index-- {
				preparations[index].Abort()
			}
		}
		a.updateAccess.Unlock()
		return nil, err
	}

	a.outboundsAccess.Lock()
	a.outbounds = orderedOutbounds
	a.outboundsByTag = outboundByTag
	a.outboundsAccess.Unlock()
	a.UpdateGroups()

	for index := len(replaced) - 1; index >= 0; index-- {
		outbound := replaced[index]
		if _, retained := u.newTagSet[outbound.Tag()]; !retained && a.history != nil {
			a.history.DeleteURLTestHistory(outbound.Tag())
		}
		if closeErr := common.Close(outbound); closeErr != nil {
			a.logger.Error(E.Cause(closeErr, "close replaced provider outbound [", outbound.Tag(), "]"))
		}
	}
	if a.enabled && a.history != nil {
		go func() {
			if _, err := a.HealthCheck(a.ctx); err != nil {
				a.logger.Debug("provider health check: ", err)
			}
		}()
	}
	a.updateAccess.Unlock()
	return append([]option.Outbound(nil), u.options...), nil
}

func (a *Adapter) UpdateOutbounds(_ []option.Outbound, newOptions []option.Outbound) ([]option.Outbound, error) {
	update, err := a.PrepareUpdateOutbounds(newOptions)
	if err != nil {
		return nil, err
	}
	return update.Commit(nil)
}

func cloneProviderOutbounds(outbounds []option.Outbound) []option.Outbound {
	cloned := make([]option.Outbound, len(outbounds))
	for index, outboundOptions := range outbounds {
		cloned[index] = outboundOptions
		value := reflect.ValueOf(outboundOptions.Options)
		if !value.IsValid() || value.Kind() != reflect.Ptr || value.IsNil() {
			continue
		}
		copyValue := reflect.New(value.Elem().Type())
		copyValue.Elem().Set(value.Elem())
		cloned[index].Options = copyValue.Interface()
	}
	return cloned
}

func (a *Adapter) providerCreationOrder(outbounds []option.Outbound) ([]int, map[string]string, error) {
	indexByTag := make(map[string]int, len(outbounds))
	for index, outboundOptions := range outbounds {
		tag := a.providerOutboundTag(outboundOptions.Tag, index)
		if _, exists := indexByTag[tag]; exists {
			return nil, nil, E.New("duplicate provider outbound tag: ", tag)
		}
		indexByTag[tag] = index
	}
	dependencies := make(map[string]string, len(outbounds))
	for index, outboundOptions := range outbounds {
		tag := a.providerOutboundTag(outboundOptions.Tag, index)
		dependency := providerDialerDetour(outboundOptions.Options)
		if _, local := indexByTag[dependency]; local {
			dependencies[tag] = dependency
		}
	}
	state := make(map[string]uint8, len(outbounds))
	order := make([]int, 0, len(outbounds))
	var visit func(string, []string) error
	visit = func(tag string, chain []string) error {
		switch state[tag] {
		case 1:
			return E.New("circular provider outbound dependency: ", F.ToString(append(chain, tag)))
		case 2:
			return nil
		}
		state[tag] = 1
		if dependency := dependencies[tag]; dependency != "" {
			if err := visit(dependency, append(chain, tag)); err != nil {
				return err
			}
		}
		state[tag] = 2
		order = append(order, indexByTag[tag])
		return nil
	}
	for index, outboundOptions := range outbounds {
		tag := a.providerOutboundTag(outboundOptions.Tag, index)
		if err := visit(tag, nil); err != nil {
			return nil, nil, err
		}
	}
	return order, dependencies, nil
}

func providerDialerDetour(options any) string {
	value := reflect.ValueOf(options)
	if !value.IsValid() || value.Kind() != reflect.Ptr || value.IsNil() {
		return ""
	}
	value = value.Elem()
	if value.Kind() != reflect.Struct {
		return ""
	}
	dialerOptions := value.FieldByName("DialerOptions")
	if !dialerOptions.IsValid() || dialerOptions.Kind() != reflect.Struct {
		return ""
	}
	detour := dialerOptions.FieldByName("Detour")
	if !detour.IsValid() || detour.Kind() != reflect.String {
		return ""
	}
	return detour.String()
}

func (a *Adapter) HealthCheck(ctx context.Context) (map[string]uint16, error) {
	a.tickerAccess.Lock()
	if a.ticker != nil {
		a.ticker.Reset(a.interval)
	}
	a.tickerAccess.Unlock()
	return a.healthcheck(ctx)
}

func (a *Adapter) RegisterCallback(callback adapter.ProviderUpdateCallback) *list.Element[adapter.ProviderUpdateCallback] {
	a.callbackAccess.Lock()
	defer a.callbackAccess.Unlock()
	return a.callbacks.PushBack(callback)
}

func (a *Adapter) UnregisterCallback(element *list.Element[adapter.ProviderUpdateCallback]) {
	if element == nil {
		return
	}
	a.callbackAccess.Lock()
	defer a.callbackAccess.Unlock()
	a.callbacks.Remove(element)
}

func (a *Adapter) RegisterPrepareCallback(callback adapter.ProviderUpdatePrepareCallback) *list.Element[adapter.ProviderUpdatePrepareCallback] {
	a.prepareCallbackAccess.Lock()
	defer a.prepareCallbackAccess.Unlock()
	return a.prepareCallbacks.PushBack(callback)
}

func (a *Adapter) UnregisterPrepareCallback(element *list.Element[adapter.ProviderUpdatePrepareCallback]) {
	if element == nil {
		return
	}
	a.prepareCallbackAccess.Lock()
	defer a.prepareCallbackAccess.Unlock()
	a.prepareCallbacks.Remove(element)
}

func (a *Adapter) prepareGroupUpdates(outbounds []adapter.Outbound) ([]adapter.ProviderUpdatePreparation, error) {
	a.prepareCallbackAccess.Lock()
	callbacks := make([]adapter.ProviderUpdatePrepareCallback, 0)
	for element := a.prepareCallbacks.Front(); element != nil; element = element.Next() {
		callbacks = append(callbacks, element.Value)
	}
	a.prepareCallbackAccess.Unlock()
	preparations := make([]adapter.ProviderUpdatePreparation, 0, len(callbacks))
	for _, callback := range callbacks {
		preparation, err := callback(a.providerTag, outbounds)
		if err != nil {
			for index := len(preparations) - 1; index >= 0; index-- {
				preparations[index].Abort()
			}
			return nil, err
		}
		if preparation != nil {
			preparations = append(preparations, preparation)
		}
	}
	return preparations, nil
}

func (a *Adapter) UpdateGroups() {
	a.callbackAccess.Lock()
	callbacks := make([]adapter.ProviderUpdateCallback, 0)
	for element := a.callbacks.Front(); element != nil; element = element.Next() {
		callbacks = append(callbacks, element.Value)
	}
	a.callbackAccess.Unlock()
	for _, callback := range callbacks {
		if err := callProviderUpdateCallback(callback, a.providerTag); err != nil {
			a.logger.Error(E.Cause(err, "update groups for provider ", a.providerTag))
		}
	}
}

func callProviderUpdateCallback(callback adapter.ProviderUpdateCallback, providerTag string) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = E.New("provider update callback panic: ", fmt.Sprint(recovered))
		}
	}()
	return callback(providerTag)
}

func (a *Adapter) Close() error {
	a.cancel()
	a.tickerAccess.Lock()
	if a.ticker != nil {
		a.ticker.Stop()
		a.ticker = nil
	}
	a.tickerAccess.Unlock()

	a.updateAccess.Lock()
	defer a.updateAccess.Unlock()
	a.outboundsAccess.Lock()
	outbounds := append([]adapter.Outbound(nil), a.outbounds...)
	a.outbounds = nil
	a.outboundsByTag = nil
	a.outboundsAccess.Unlock()

	return a.removeProviderOutbounds(outbounds)
}

func (a *Adapter) loopCheck() {
	ticker := time.NewTicker(a.interval)
	a.tickerAccess.Lock()
	a.ticker = ticker
	a.tickerAccess.Unlock()
	defer func() {
		ticker.Stop()
		a.tickerAccess.Lock()
		if a.ticker == ticker {
			a.ticker = nil
		}
		a.tickerAccess.Unlock()
	}()
	_, _ = a.healthcheck(a.ctx)
	for {
		select {
		case <-a.ctx.Done():
			return
		case <-ticker.C:
			_, _ = a.healthcheck(a.ctx)
		}
	}
}

func (a *Adapter) healthcheck(ctx context.Context) (map[string]uint16, error) {
	result := make(map[string]uint16)
	if a.checking.Swap(true) {
		return result, nil
	}
	defer a.checking.Store(false)
	if a.history == nil {
		return result, nil
	}
	outbounds := a.Outbounds()
	batchGroup, _ := batch.New(ctx, batch.WithConcurrencyNum[any](10))
	var resultAccess sync.Mutex
	checked := make(map[string]bool)
	for _, detour := range outbounds {
		detour := detour
		tag := detour.Tag()
		if checked[tag] {
			continue
		}
		checked[tag] = true
		batchGroup.Go(tag, func() (any, error) {
			testContext, cancel := context.WithTimeout(ctx, a.timeout)
			defer cancel()
			delay, err := urltest.URLTest(testContext, a.link, detour)
			if err != nil {
				a.logger.Debug("outbound ", tag, " unavailable: ", err)
				a.history.DeleteURLTestHistory(tag)
			} else {
				a.logger.Debug("outbound ", tag, " available: ", delay, "ms")
				a.history.StoreURLTestHistory(tag, &urltest.History{Time: time.Now(), Delay: delay})
				resultAccess.Lock()
				result[tag] = delay
				resultAccess.Unlock()
			}
			return nil, nil
		})
	}
	batchGroup.Wait()
	return result, nil
}

func (a *Adapter) removeProviderOutbounds(outbounds []adapter.Outbound) error {
	pending := append([]adapter.Outbound(nil), outbounds...)
	lastErrors := make(map[string]error, len(pending))
	for len(pending) > 0 {
		next := make([]adapter.Outbound, 0, len(pending))
		removed := 0
		for index := len(pending) - 1; index >= 0; index-- {
			outbound := pending[index]
			if err := a.outbound.Remove(outbound.Tag()); err != nil {
				lastErrors[outbound.Tag()] = err
				next = append(next, outbound)
				continue
			}
			delete(lastErrors, outbound.Tag())
			if a.history != nil {
				a.history.DeleteURLTestHistory(outbound.Tag())
			}
			removed++
		}
		if removed == 0 {
			var result error
			for _, outbound := range next {
				result = E.Errors(result, E.Cause(lastErrors[outbound.Tag()], "remove provider outbound [", outbound.Tag(), "]"))
			}
			return result
		}
		pending = next
	}
	return nil
}
