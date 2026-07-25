package provider

import (
	"context"
	"reflect"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/urltest"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	providerParser "github.com/sagernet/sing-box/provider/parser"
	"github.com/sagernet/sing/common/batch"
	E "github.com/sagernet/sing/common/exceptions"
	F "github.com/sagernet/sing/common/format"
	"github.com/sagernet/sing/common/x/list"
	"github.com/sagernet/sing/service"
)

type Adapter struct {
	ctx          context.Context
	outbound     adapter.OutboundManager
	router       adapter.Router
	logFactory   log.Factory
	logger       log.ContextLogger
	providerType string
	providerTag  string

	updateAccess    sync.Mutex
	outboundsAccess sync.RWMutex
	outbounds       []adapter.Outbound
	outboundsByTag  map[string]adapter.Outbound

	tickerAccess sync.Mutex
	ticker       *time.Ticker
	checking     atomic.Bool
	history      *urltest.HistoryStorage

	callbackAccess sync.Mutex
	callbacks      list.List[adapter.ProviderUpdateCallback]

	link     string
	enabled  bool
	timeout  time.Duration
	interval time.Duration
}

func NewAdapter(ctx context.Context, router adapter.Router, outbound adapter.OutboundManager, logFactory log.Factory, logger log.ContextLogger, providerTag string, providerType string, options option.ProviderHealthCheckOptions) Adapter {
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
		ctx:          ctx,
		outbound:     outbound,
		router:       router,
		logFactory:   logFactory,
		logger:       logger,
		providerType: providerType,
		providerTag:  providerTag,
		enabled:      options.Enabled,
		link:         options.URL,
		timeout:      timeout,
		interval:     interval,
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

func providerOutboundTag(providerTag string, optionTag string, index int) string {
	if optionTag != "" {
		return F.ToString(providerTag, "/", optionTag)
	}
	return F.ToString(providerTag, "/", index)
}

func (a *Adapter) UpdateOutbounds(oldOptions []option.Outbound, newOptions []option.Outbound) ([]option.Outbound, error) {
	a.updateAccess.Lock()
	defer a.updateAccess.Unlock()

	// Keep provider state and cache options in their canonical, unprefixed form.
	// Only the options passed to the outbound manager are cloned and rewritten,
	// so a cached provider cannot acquire the provider prefix more than once.
	preparedOptions := cloneProviderOutbounds(newOptions)
	providerParser.PrefixProviderDetours(a.providerTag, preparedOptions)

	oldOptionByTag := make(map[string]option.Outbound, len(oldOptions))
	for index, outboundOptions := range oldOptions {
		oldOptionByTag[providerOutboundTag(a.providerTag, outboundOptions.Tag, index)] = outboundOptions
	}
	newOptionByTag := make(map[string]option.Outbound, len(newOptions))
	newIndexByTag := make(map[string]int, len(newOptions))
	for index, outboundOptions := range newOptions {
		tag := providerOutboundTag(a.providerTag, outboundOptions.Tag, index)
		if _, exists := newIndexByTag[tag]; exists {
			return nil, E.New("duplicate provider outbound tag: ", tag)
		}
		newOptionByTag[tag] = outboundOptions
		newIndexByTag[tag] = index
	}

	creationOrder, dependencies, err := providerCreationOrder(a.providerTag, preparedOptions)
	if err != nil {
		return nil, err
	}
	ownChanged := make(map[string]bool, len(newOptions))
	for tag, outboundOptions := range newOptionByTag {
		oldOptions, existedBefore := oldOptionByTag[tag]
		_, exists := a.outbound.Outbound(tag)
		ownChanged[tag] = !exists || !existedBefore || !reflect.DeepEqual(outboundOptions, oldOptions)
	}

	outboundByTag := make(map[string]adapter.Outbound, len(newOptions))
	effectiveOptionByTag := make(map[string]option.Outbound, len(newOptions))
	replaced := make(map[string]bool, len(newOptions))
	failed := make(map[string]bool, len(newOptions))
	var updateErr error
	for _, index := range creationOrder {
		outboundOptions := preparedOptions[index]
		canonicalOptions := newOptions[index]
		tag := providerOutboundTag(a.providerTag, canonicalOptions.Tag, index)
		dependency := dependencies[tag]
		outbound, exists := a.outbound.Outbound(tag)
		if dependency != "" && failed[dependency] {
			failed[tag] = true
			updateErr = E.Errors(updateErr, E.New("skip provider outbound ", tag, ": dependency update failed: ", dependency))
			continue
		}
		dependencyReplaced := dependency != "" && replaced[dependency]
		if ownChanged[tag] || dependencyReplaced {
			createErr := a.outbound.Create(
				adapter.WithContext(a.ctx, &adapter.InboundContext{Outbound: tag}),
				a.router,
				a.logFactory.NewLogger(F.ToString("outbound/", outboundOptions.Type, "[", tag, "]")),
				tag,
				outboundOptions.Type,
				outboundOptions.Options,
			)
			if createErr != nil {
				updateErr = E.Errors(updateErr, E.Cause(createErr, "create provider outbound ", tag))
				if exists && !dependencyReplaced {
					outboundByTag[tag] = outbound
					if oldOptions, loaded := oldOptionByTag[tag]; loaded {
						effectiveOptionByTag[tag] = oldOptions
					}
				} else {
					failed[tag] = true
				}
				continue
			}
			replaced[tag] = true
			outbound, exists = a.outbound.Outbound(tag)
		}
		if !exists || outbound == nil {
			failed[tag] = true
			updateErr = E.Errors(updateErr, E.New("provider outbound not found after creation: ", tag))
			continue
		}
		outboundByTag[tag] = outbound
		effectiveOptionByTag[tag] = canonicalOptions
	}

	// When a dependency was replaced, an old dependent cannot safely remain in
	// the manager after its recreation fails because it still holds the closed
	// dependency object. Remove failed branches from leaves to roots.
	failedOutbounds := make([]adapter.Outbound, 0, len(failed))
	for index := len(creationOrder) - 1; index >= 0; index-- {
		optionIndex := creationOrder[index]
		tag := providerOutboundTag(a.providerTag, newOptions[optionIndex].Tag, optionIndex)
		if !failed[tag] {
			continue
		}
		if outbound, exists := a.outbound.Outbound(tag); exists && outbound != nil {
			failedOutbounds = append(failedOutbounds, outbound)
		}
	}
	if removeErr := a.removeProviderOutbounds(failedOutbounds); removeErr != nil {
		updateErr = E.Errors(updateErr, removeErr)
	}

	currentOutbounds := a.Outbounds()
	obsolete := make([]adapter.Outbound, 0)
	for _, outbound := range currentOutbounds {
		if _, exists := newIndexByTag[outbound.Tag()]; !exists {
			obsolete = append(obsolete, outbound)
		}
	}
	if removeErr := a.removeProviderOutbounds(obsolete); removeErr != nil {
		updateErr = E.Errors(updateErr, removeErr)
		for _, outbound := range obsolete {
			remaining, exists := a.outbound.Outbound(outbound.Tag())
			if !exists || remaining == nil {
				continue
			}
			outboundByTag[outbound.Tag()] = remaining
			if oldOptions, loaded := oldOptionByTag[outbound.Tag()]; loaded {
				effectiveOptionByTag[outbound.Tag()] = oldOptions
			}
		}
	}

	outbounds := make([]adapter.Outbound, 0, len(outboundByTag))
	effectiveOptions := make([]option.Outbound, 0, len(effectiveOptionByTag))
	for index, outboundOptions := range newOptions {
		tag := providerOutboundTag(a.providerTag, outboundOptions.Tag, index)
		if outbound := outboundByTag[tag]; outbound != nil {
			outbounds = append(outbounds, outbound)
			effectiveOptions = append(effectiveOptions, effectiveOptionByTag[tag])
		}
	}
	for _, outbound := range obsolete {
		if _, stillPresent := outboundByTag[outbound.Tag()]; !stillPresent {
			continue
		}
		if _, inNew := newIndexByTag[outbound.Tag()]; inNew {
			continue
		}
		outbounds = append(outbounds, outbound)
		effectiveOptions = append(effectiveOptions, effectiveOptionByTag[outbound.Tag()])
	}

	a.outboundsAccess.Lock()
	a.outbounds = outbounds
	a.outboundsByTag = outboundByTag
	a.outboundsAccess.Unlock()

	if a.enabled && a.history != nil {
		go func() {
			if _, err := a.HealthCheck(a.ctx); err != nil {
				a.logger.Debug("provider health check: ", err)
			}
		}()
	}
	return effectiveOptions, updateErr
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

func providerCreationOrder(providerTag string, outbounds []option.Outbound) ([]int, map[string]string, error) {
	indexByTag := make(map[string]int, len(outbounds))
	for index, outboundOptions := range outbounds {
		tag := providerOutboundTag(providerTag, outboundOptions.Tag, index)
		if _, exists := indexByTag[tag]; exists {
			return nil, nil, E.New("duplicate provider outbound tag: ", tag)
		}
		indexByTag[tag] = index
	}
	dependencies := make(map[string]string, len(outbounds))
	for index, outboundOptions := range outbounds {
		tag := providerOutboundTag(providerTag, outboundOptions.Tag, index)
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
		tag := providerOutboundTag(providerTag, outboundOptions.Tag, index)
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

func (a *Adapter) UpdateGroups() {
	a.callbackAccess.Lock()
	callbacks := make([]adapter.ProviderUpdateCallback, 0)
	for element := a.callbacks.Front(); element != nil; element = element.Next() {
		callbacks = append(callbacks, element.Value)
	}
	a.callbackAccess.Unlock()
	for _, callback := range callbacks {
		if err := callback(a.providerTag); err != nil {
			a.logger.Error(E.Cause(err, "update groups for provider ", a.providerTag))
		}
	}
}

func (a *Adapter) Close() error {
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
