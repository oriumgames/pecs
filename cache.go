package pecs

import (
	"context"
	"reflect"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"
)

// peerCache manages cached data for remote players.
type peerCache struct {
	manager *Manager

	// entries maps playerID -> *peerCacheEntry
	entries sync.Map

	// providerIndex maps component type -> []PeerProvider
	providerIndex   map[reflect.Type][]PeerProvider
	providerIndexMu sync.RWMutex

	// providers holds all registered player providers
	providers   []peerProviderEntry
	providersMu sync.RWMutex

	// cleanupInterval is how often to run cache cleanup
	cleanupInterval time.Duration
	stopCleanup     chan struct{}

	// workerSem limits concurrent background goroutines
	workerSem chan struct{}

	// ctx and cancel for graceful shutdown of all background operations
	ctx    context.Context
	cancel context.CancelFunc
}

type peerProviderEntry struct {
	provider PeerProvider
	options  ProviderOptions
}

// peerCacheEntry holds cached data for a single remote player.
type peerCacheEntry struct {
	playerID string
	cache    *peerCache

	// components maps component type -> unsafe.Pointer to data
	components sync.Map

	// lastAccessedAt is when the data was last accessed (unix millis)
	lastAccessedAt atomic.Int64

	// fetchedAt is when the data was last fetched (unix millis)
	fetchedAt atomic.Int64

	// status: 0=pending, 1=ready, 2=error, 3=closing
	status atomic.Uint32

	// errorAt is when the last error occurred (unix millis), used for retry logic
	errorAt atomic.Int64

	// subscriptions holds active provider subscriptions
	subscriptions   []Subscription
	subscriptionsMu sync.Mutex

	// updateCh receives updates from providers
	updateCh chan PlayerUpdate

	// mu protects subscription setup
	mu sync.Mutex
}

// Cache configuration constants
const (
	// cacheCleanupInterval is how often the cache cleanup runs
	cacheCleanupInterval = 10 * time.Second

	// cacheGracePeriodMs is how long to keep entries after last reference (milliseconds)
	cacheGracePeriodMs = 30_000

	// cacheFetchTimeout is the timeout for provider fetch operations
	cacheFetchTimeout = 5 * time.Second

	// cacheUpdateChannelSize is the buffer size for update channels
	cacheUpdateChannelSize = 16

	// cacheMaxWorkers limits concurrent background goroutines per cache
	// Set high enough to not throttle normal operation, but prevents unbounded growth
	cacheMaxWorkers = 1000

	// cacheRetryDelayMs is how long to wait before retrying a failed fetch (milliseconds)
	cacheRetryDelayMs = 5_000
)

const (
	cacheStatusPending = iota
	cacheStatusReady
	cacheStatusError
	cacheStatusClosing
)

// newPeerCache creates a new peer cache.
func newPeerCache(manager *Manager) *peerCache {
	ctx, cancel := context.WithCancel(context.Background())
	pc := &peerCache{
		manager:         manager,
		providerIndex:   make(map[reflect.Type][]PeerProvider),
		cleanupInterval: cacheCleanupInterval,
		stopCleanup:     make(chan struct{}),
		workerSem:       make(chan struct{}, cacheMaxWorkers),
		ctx:             ctx,
		cancel:          cancel,
	}
	go pc.cleanupLoop()
	return pc
}

// spawnWorker runs fn in a goroutine if capacity is available, otherwise runs inline.
// Checks for shutdown before spawning to prevent goroutine leaks during shutdown.
func (pc *peerCache) spawnWorker(fn func()) {
	// Check if shutting down
	select {
	case <-pc.ctx.Done():
		return // Don't spawn new work during shutdown
	default:
	}

	select {
	case pc.workerSem <- struct{}{}:
		go func() {
			defer func() { <-pc.workerSem }()
			// Check again in case shutdown happened while waiting
			select {
			case <-pc.ctx.Done():
				return
			default:
				fn()
			}
		}()
	default:
		// Semaphore full - run inline to avoid blocking
		fn()
	}
}

// registerProvider adds a player provider to the cache.
func (pc *peerCache) registerProvider(p PeerProvider, opts ProviderOptions) {
	pc.providersMu.Lock()
	pc.providers = append(pc.providers, peerProviderEntry{p, opts})
	pc.providersMu.Unlock()

	// Index by component type
	pc.providerIndexMu.Lock()
	for _, t := range p.PlayerComponents() {
		pc.providerIndex[t] = append(pc.providerIndex[t], p)
	}
	pc.providerIndexMu.Unlock()
}

// getProviders returns providers that handle the given component type.
func (pc *peerCache) getProviders(componentType reflect.Type) []PeerProvider {
	pc.providerIndexMu.RLock()
	defer pc.providerIndexMu.RUnlock()
	return pc.providerIndex[componentType]
}

// getAllProviders returns all registered provider entries.
func (pc *peerCache) getAllProviders() []peerProviderEntry {
	pc.providersMu.RLock()
	defer pc.providersMu.RUnlock()
	result := make([]peerProviderEntry, len(pc.providers))
	copy(result, pc.providers)
	return result
}

// resolve gets or creates a cache entry for a player and returns their component.
// Returns nil if the player doesn't have the component or resolution fails.
func (pc *peerCache) resolve(playerID string, componentType reflect.Type) unsafe.Pointer {
	if playerID == "" {
		return nil
	}

	entry := pc.getOrCreateEntry(playerID)
	status := entry.status.Load()

	// Check if error state should be reset for retry
	if status == cacheStatusError {
		now := time.Now().UnixMilli()
		if now-entry.errorAt.Load() > cacheRetryDelayMs {
			if entry.status.CompareAndSwap(cacheStatusError, cacheStatusPending) {
				status = cacheStatusPending
			}
		}
	}

	// Fetch if pending
	if status == cacheStatusPending {
		entry.mu.Lock()
		if entry.status.Load() == cacheStatusPending {
			pc.fetchAndSubscribe(entry, componentType)
		}
		entry.mu.Unlock()
	}

	// Check if ready
	if entry.status.Load() != cacheStatusReady {
		return nil
	}

	// Get component
	if val, ok := entry.components.Load(componentType); ok {
		entry.lastAccessedAt.Store(time.Now().UnixMilli())
		return val.(unsafe.Pointer)
	}
	return nil
}

// resolveMany resolves multiple player IDs and returns their components.
// Uses batch fetching for efficiency.
func (pc *peerCache) resolveMany(playerIDs []string, componentType reflect.Type) []unsafe.Pointer {
	if len(playerIDs) == 0 {
		return nil
	}

	results := make([]unsafe.Pointer, len(playerIDs))
	var toFetch []string
	var toFetchIndices []int
	entries := make([]*peerCacheEntry, len(playerIDs))

	// First pass: check existing entries and collect what needs fetching
	for i, id := range playerIDs {
		if id == "" {
			continue
		}

		entry := pc.getOrCreateEntry(id)
		entries[i] = entry

		switch entry.status.Load() {
		case cacheStatusReady:
			if val, ok := entry.components.Load(componentType); ok {
				entry.lastAccessedAt.Store(time.Now().UnixMilli())
				results[i] = val.(unsafe.Pointer)
			}
		case cacheStatusPending:
			toFetch = append(toFetch, id)
			toFetchIndices = append(toFetchIndices, i)
		}
	}

	// Batch fetch pending entries
	if len(toFetch) > 0 {
		pc.batchFetch(toFetch, toFetchIndices, entries, componentType, results)
	}

	return results
}

// getOrCreateEntry gets or creates a cache entry for a player.
func (pc *peerCache) getOrCreateEntry(playerID string) *peerCacheEntry {
	if val, ok := pc.entries.Load(playerID); ok {
		return val.(*peerCacheEntry)
	}

	entry := &peerCacheEntry{
		playerID: playerID,
		cache:    pc,
		updateCh: make(chan PlayerUpdate, cacheUpdateChannelSize),
	}

	actual, loaded := pc.entries.LoadOrStore(playerID, entry)
	if loaded {
		return actual.(*peerCacheEntry)
	}

	// Start update processor
	pc.spawnWorker(entry.processUpdates)

	return entry
}

// fetchAndSubscribe fetches initial data and sets up subscriptions.
func (pc *peerCache) fetchAndSubscribe(entry *peerCacheEntry, componentType reflect.Type) {
	ctx, cancel := context.WithTimeout(context.Background(), cacheFetchTimeout)
	defer cancel()

	providers := pc.getProviders(componentType)
	if len(providers) == 0 {
		entry.errorAt.Store(time.Now().UnixMilli())
		entry.status.Store(cacheStatusError)
		return
	}

	var wg sync.WaitGroup
	var anySuccess atomic.Bool

	for _, p := range providers {
		wg.Add(1)
		go func(provider PeerProvider) {
			defer wg.Done()

			// Fetch initial data
			components, err := provider.FetchPlayer(ctx, entry.playerID)
			if err != nil {
				return
			}

			// Store components
			for _, comp := range components {
				if comp == nil {
					continue
				}
				t := reflect.TypeOf(comp)
				if t.Kind() == reflect.Ptr {
					t = t.Elem()
				}
				entry.components.Store(t, ptrValueRaw(comp))
				anySuccess.Store(true)
			}

			// Subscribe for updates
			sub, err := provider.SubscribePlayer(ctx, entry.playerID, entry.updateCh)
			if err != nil {
				return
			}

			entry.subscriptionsMu.Lock()
			entry.subscriptions = append(entry.subscriptions, sub)
			entry.subscriptionsMu.Unlock()
		}(p)
	}

	wg.Wait()

	entry.fetchedAt.Store(time.Now().UnixMilli())

	if anySuccess.Load() {
		entry.status.Store(cacheStatusReady)
	} else {
		entry.errorAt.Store(time.Now().UnixMilli())
		entry.status.Store(cacheStatusError)
	}
}

// batchFetch fetches multiple players at once.
func (pc *peerCache) batchFetch(playerIDs []string, indices []int, entries []*peerCacheEntry, componentType reflect.Type, results []unsafe.Pointer) {
	ctx, cancel := context.WithTimeout(context.Background(), cacheFetchTimeout)
	defer cancel()

	providers := pc.getProviders(componentType)
	if len(providers) == 0 {
		return
	}

	// Lock all entries we're fetching
	for _, idx := range indices {
		entries[idx].mu.Lock()
	}
	defer func() {
		for _, idx := range indices {
			entries[idx].mu.Unlock()
		}
	}()

	// Use batch API from first provider that supports it
	for _, p := range providers {
		componentsMap, err := p.FetchPlayers(ctx, playerIDs)
		if err != nil {
			continue
		}

		now := time.Now().UnixMilli()

		for i, id := range playerIDs {
			idx := indices[i]
			entry := entries[idx]

			if entry.status.Load() != cacheStatusPending {
				continue
			}

			components, ok := componentsMap[id]
			if !ok || len(components) == 0 {
				entry.errorAt.Store(now)
				entry.status.Store(cacheStatusError)
				continue
			}

			// Store components
			for _, comp := range components {
				if comp == nil {
					continue
				}
				t := reflect.TypeOf(comp)
				if t.Kind() == reflect.Ptr {
					t = t.Elem()
				}
				entry.components.Store(t, ptrValueRaw(comp))

				if t == componentType {
					results[idx] = ptrValueRaw(comp)
				}
			}

			entry.fetchedAt.Store(now)
			entry.status.Store(cacheStatusReady)

			// Subscribe for updates (async)
			capturedEntry := entry
			capturedProvider := p
			pc.spawnWorker(func() { pc.subscribeEntry(capturedEntry, capturedProvider) })
		}

		break // Only use first provider
	}
}

// subscribeEntry sets up subscription for an entry.
func (pc *peerCache) subscribeEntry(entry *peerCacheEntry, provider PeerProvider) {
	// Use cache's context so subscriptions are cancelled on shutdown
	sub, err := provider.SubscribePlayer(pc.ctx, entry.playerID, entry.updateCh)
	if err != nil {
		return
	}

	entry.subscriptionsMu.Lock()
	entry.subscriptions = append(entry.subscriptions, sub)
	entry.subscriptionsMu.Unlock()
}

// processUpdates handles incoming updates for a cache entry.
func (e *peerCacheEntry) processUpdates() {
	for update := range e.updateCh {
		if e.status.Load() == cacheStatusClosing {
			return
		}

		if update.Data == nil {
			// Remove component
			e.components.Delete(update.ComponentType)
		} else {
			// Update component
			t := update.ComponentType
			if t.Kind() == reflect.Ptr {
				t = t.Elem()
			}
			e.components.Store(t, ptrValueRaw(update.Data))
		}

		e.fetchedAt.Store(time.Now().UnixMilli())
	}
}

// close shuts down the entry and cleans up resources.
func (e *peerCacheEntry) close() {
	if !e.status.CompareAndSwap(cacheStatusReady, cacheStatusClosing) &&
		!e.status.CompareAndSwap(cacheStatusPending, cacheStatusClosing) &&
		!e.status.CompareAndSwap(cacheStatusError, cacheStatusClosing) {
		return // Already closing
	}

	e.subscriptionsMu.Lock()
	subs := e.subscriptions
	e.subscriptions = nil
	e.subscriptionsMu.Unlock()

	for _, sub := range subs {
		sub.Close()
	}

	close(e.updateCh)
}

// cleanupLoop periodically cleans up unused cache entries.
func (pc *peerCache) cleanupLoop() {
	ticker := time.NewTicker(pc.cleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-pc.stopCleanup:
			return
		case <-ticker.C:
			pc.cleanup()
		}
	}
}

// cleanup removes stale entries past their grace period.
func (pc *peerCache) cleanup() {
	now := time.Now().UnixMilli()

	pc.entries.Range(func(key, value any) bool {
		entry := value.(*peerCacheEntry)

		// Use lastAccessedAt if set, otherwise fall back to fetchedAt
		lastAccess := entry.lastAccessedAt.Load()
		if lastAccess == 0 {
			lastAccess = entry.fetchedAt.Load()
		}

		age := now - lastAccess
		if age > cacheGracePeriodMs {
			entry.close()
			pc.entries.Delete(key)
		}

		return true
	})
}

// stop shuts down the peer cache.
func (pc *peerCache) stop() {
	// Cancel context first to stop all background operations
	pc.cancel()

	close(pc.stopCleanup)

	pc.entries.Range(func(key, value any) bool {
		entry := value.(*peerCacheEntry)
		entry.close()
		return true
	})
}

// sharedCache manages cached data for shared entities.
type sharedCache struct {
	manager *Manager

	// entries maps entityID -> *sharedCacheEntry
	entries sync.Map

	// providerIndex maps data type -> []SharedProvider
	providerIndex   map[reflect.Type][]SharedProvider
	providerIndexMu sync.RWMutex

	// providers holds all registered entity providers
	providers   []sharedProviderEntry
	providersMu sync.RWMutex

	// cleanupInterval is how often to run cache cleanup
	cleanupInterval time.Duration
	stopCleanup     chan struct{}

	// workerSem limits concurrent background goroutines
	workerSem chan struct{}

	// ctx and cancel for graceful shutdown of all background operations
	ctx    context.Context
	cancel context.CancelFunc
}

type sharedProviderEntry struct {
	provider SharedProvider
	options  ProviderOptions
}

// sharedCacheEntry holds cached data for a single shared entity.
type sharedCacheEntry struct {
	entityID string
	cache    *sharedCache

	// data holds the entity data
	data atomic.Pointer[any]

	// dataType is the type of the stored data
	dataType reflect.Type

	// lastAccessedAt is when the data was last accessed (unix millis)
	lastAccessedAt atomic.Int64

	// fetchedAt is when the data was last fetched (unix millis)
	fetchedAt atomic.Int64

	// status: 0=pending, 1=ready, 2=error, 3=closing
	status atomic.Uint32

	// errorAt is when the last error occurred (unix millis), used for retry logic
	errorAt atomic.Int64

	// subscription holds the active provider subscription
	subscription   Subscription
	subscriptionMu sync.Mutex

	// updateCh receives updates from provider
	updateCh chan any

	// mu protects subscription setup
	mu sync.Mutex
}

// newSharedCache creates a new shared cache.
func newSharedCache(manager *Manager) *sharedCache {
	ctx, cancel := context.WithCancel(context.Background())
	sc := &sharedCache{
		manager:         manager,
		providerIndex:   make(map[reflect.Type][]SharedProvider),
		cleanupInterval: cacheCleanupInterval,
		stopCleanup:     make(chan struct{}),
		workerSem:       make(chan struct{}, cacheMaxWorkers),
		ctx:             ctx,
		cancel:          cancel,
	}
	go sc.cleanupLoop()
	return sc
}

// spawnWorker runs fn in a goroutine if capacity is available, otherwise runs inline.
// Checks for shutdown before spawning to prevent goroutine leaks during shutdown.
func (sc *sharedCache) spawnWorker(fn func()) {
	// Check if shutting down
	select {
	case <-sc.ctx.Done():
		return // Don't spawn new work during shutdown
	default:
	}

	select {
	case sc.workerSem <- struct{}{}:
		go func() {
			defer func() { <-sc.workerSem }()
			// Check again in case shutdown happened while waiting
			select {
			case <-sc.ctx.Done():
				return
			default:
				fn()
			}
		}()
	default:
		fn()
	}
}

// registerProvider adds an entity provider to the cache.
func (sc *sharedCache) registerProvider(p SharedProvider, opts ProviderOptions) {
	sc.providersMu.Lock()
	sc.providers = append(sc.providers, sharedProviderEntry{p, opts})
	sc.providersMu.Unlock()

	// Index by data type
	sc.providerIndexMu.Lock()
	for _, t := range p.EntityComponents() {
		sc.providerIndex[t] = append(sc.providerIndex[t], p)
	}
	sc.providerIndexMu.Unlock()
}

// getProviders returns providers that handle the given data type.
func (sc *sharedCache) getProviders(dataType reflect.Type) []SharedProvider {
	sc.providerIndexMu.RLock()
	defer sc.providerIndexMu.RUnlock()
	return sc.providerIndex[dataType]
}

// resolve gets or creates a cache entry for an entity and returns its data.
func (sc *sharedCache) resolve(entityID string, dataType reflect.Type) unsafe.Pointer {
	if entityID == "" {
		return nil
	}

	entry := sc.getOrCreateEntry(entityID, dataType)
	status := entry.status.Load()

	// Check if error state should be reset for retry
	if status == cacheStatusError {
		now := time.Now().UnixMilli()
		if now-entry.errorAt.Load() > cacheRetryDelayMs {
			if entry.status.CompareAndSwap(cacheStatusError, cacheStatusPending) {
				status = cacheStatusPending
			}
		}
	}

	// Fetch if pending
	if status == cacheStatusPending {
		entry.mu.Lock()
		if entry.status.Load() == cacheStatusPending {
			sc.fetchAndSubscribe(entry, dataType)
		}
		entry.mu.Unlock()
	}

	// Check if ready
	if entry.status.Load() != cacheStatusReady {
		return nil
	}

	// Get data
	dataPtr := entry.data.Load()
	if dataPtr == nil {
		return nil
	}
	entry.lastAccessedAt.Store(time.Now().UnixMilli())
	return ptrValueRaw(*dataPtr)
}

// resolveMany resolves multiple entity IDs and returns their data.
func (sc *sharedCache) resolveMany(entityIDs []string, dataType reflect.Type) []unsafe.Pointer {
	if len(entityIDs) == 0 {
		return nil
	}

	results := make([]unsafe.Pointer, len(entityIDs))
	var toFetch []string
	var toFetchIndices []int
	entries := make([]*sharedCacheEntry, len(entityIDs))

	// First pass: check existing entries and collect what needs fetching
	for i, id := range entityIDs {
		if id == "" {
			continue
		}

		entry := sc.getOrCreateEntry(id, dataType)
		entries[i] = entry

		switch entry.status.Load() {
		case cacheStatusReady:
			if dataPtr := entry.data.Load(); dataPtr != nil {
				entry.lastAccessedAt.Store(time.Now().UnixMilli())
				results[i] = ptrValueRaw(*dataPtr)
			}
		case cacheStatusPending:
			toFetch = append(toFetch, id)
			toFetchIndices = append(toFetchIndices, i)
		}
	}

	// Batch fetch pending entries
	if len(toFetch) > 0 {
		sc.batchFetch(toFetch, toFetchIndices, entries, dataType, results)
	}

	return results
}

// batchFetch fetches multiple entities at once.
func (sc *sharedCache) batchFetch(entityIDs []string, indices []int, entries []*sharedCacheEntry, dataType reflect.Type, results []unsafe.Pointer) {
	ctx, cancel := context.WithTimeout(context.Background(), cacheFetchTimeout)
	defer cancel()

	providers := sc.getProviders(dataType)
	if len(providers) == 0 {
		return
	}

	// Lock all entries we're fetching
	for _, idx := range indices {
		entries[idx].mu.Lock()
	}
	defer func() {
		for _, idx := range indices {
			if entries[idx] != nil {
				entries[idx].mu.Unlock()
			}
		}
	}()

	// Use batch API from first provider that supports it
	for _, p := range providers {
		dataMap, err := p.FetchEntities(ctx, entityIDs)
		if err != nil {
			continue // Try next provider
		}

		now := time.Now().UnixMilli()

		for i, id := range entityIDs {
			idx := indices[i]
			entry := entries[idx]

			if entry.status.Load() != cacheStatusPending {
				continue
			}

			data, ok := dataMap[id]
			if !ok || data == nil {
				entry.errorAt.Store(now)
				entry.status.Store(cacheStatusError)
				continue
			}

			// Store data
			entry.data.Store(&data)
			results[idx] = ptrValueRaw(data)

			entry.fetchedAt.Store(now)
			entry.status.Store(cacheStatusReady)

			// Subscribe for updates (async)
			capturedEntry := entry
			capturedProvider := p
			sc.spawnWorker(func() { sc.subscribeEntry(capturedEntry, capturedProvider) })
		}

		return // Success, don't try other providers
	}

	// If we got here, all providers failed
	for _, idx := range indices {
		if entries[idx].status.Load() == cacheStatusPending {
			entries[idx].status.Store(cacheStatusError)
		}
	}
}

// subscribeEntry sets up subscription for an entry.
func (sc *sharedCache) subscribeEntry(entry *sharedCacheEntry, provider SharedProvider) {
	// Use cache's context so subscriptions are cancelled on shutdown
	sub, err := provider.SubscribeEntity(sc.ctx, entry.entityID, entry.updateCh)
	if err != nil {
		return
	}

	entry.subscriptionMu.Lock()
	entry.subscription = sub
	entry.subscriptionMu.Unlock()
}

// getOrCreateEntry gets or creates a cache entry for an entity.
func (sc *sharedCache) getOrCreateEntry(entityID string, dataType reflect.Type) *sharedCacheEntry {
	// Key includes both entityID and type for type-specific caching
	key := entityID + ":" + dataType.String()

	if val, ok := sc.entries.Load(key); ok {
		return val.(*sharedCacheEntry)
	}

	entry := &sharedCacheEntry{
		entityID: entityID,
		cache:    sc,
		dataType: dataType,
		updateCh: make(chan any, cacheUpdateChannelSize),
	}

	actual, loaded := sc.entries.LoadOrStore(key, entry)
	if loaded {
		return actual.(*sharedCacheEntry)
	}

	// Start update processor
	sc.spawnWorker(entry.processUpdates)

	return entry
}

// fetchAndSubscribe fetches initial data and sets up subscription.
func (sc *sharedCache) fetchAndSubscribe(entry *sharedCacheEntry, dataType reflect.Type) {
	ctx, cancel := context.WithTimeout(context.Background(), cacheFetchTimeout)
	defer cancel()

	providers := sc.getProviders(dataType)
	if len(providers) == 0 {
		entry.errorAt.Store(time.Now().UnixMilli())
		entry.status.Store(cacheStatusError)
		return
	}

	// Try each provider until one succeeds
	for _, p := range providers {
		data, err := p.FetchEntity(ctx, entry.entityID)
		if err != nil || data == nil {
			continue
		}

		// Store data
		entry.data.Store(&data)
		entry.fetchedAt.Store(time.Now().UnixMilli())
		entry.status.Store(cacheStatusReady)

		// Subscribe for updates
		capturedProvider := p
		sc.spawnWorker(func() {
			sub, err := capturedProvider.SubscribeEntity(sc.ctx, entry.entityID, entry.updateCh)
			if err != nil {
				return
			}

			entry.subscriptionMu.Lock()
			entry.subscription = sub
			entry.subscriptionMu.Unlock()
		})

		return
	}

	entry.errorAt.Store(time.Now().UnixMilli())
	entry.status.Store(cacheStatusError)
}

// processUpdates handles incoming updates for a cache entry.
func (e *sharedCacheEntry) processUpdates() {
	for update := range e.updateCh {
		if e.status.Load() == cacheStatusClosing {
			return
		}

		if update == nil {
			// Entity deleted
			e.data.Store(nil)
			e.status.Store(cacheStatusError)
		} else {
			// Update data
			e.data.Store(&update)
			e.fetchedAt.Store(time.Now().UnixMilli())
		}
	}
}

// close shuts down the entry and cleans up resources.
func (e *sharedCacheEntry) close() {
	if !e.status.CompareAndSwap(cacheStatusReady, cacheStatusClosing) &&
		!e.status.CompareAndSwap(cacheStatusPending, cacheStatusClosing) &&
		!e.status.CompareAndSwap(cacheStatusError, cacheStatusClosing) {
		return // Already closing
	}

	e.subscriptionMu.Lock()
	sub := e.subscription
	e.subscription = nil
	e.subscriptionMu.Unlock()

	if sub != nil {
		sub.Close()
	}

	close(e.updateCh)
}

// cleanupLoop periodically cleans up unused cache entries.
func (sc *sharedCache) cleanupLoop() {
	ticker := time.NewTicker(sc.cleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-sc.stopCleanup:
			return
		case <-ticker.C:
			sc.cleanup()
		}
	}
}

// cleanup removes stale entries past their grace period.
func (sc *sharedCache) cleanup() {
	now := time.Now().UnixMilli()

	sc.entries.Range(func(key, value any) bool {
		entry := value.(*sharedCacheEntry)

		// Use lastAccessedAt if set, otherwise fall back to fetchedAt
		lastAccess := entry.lastAccessedAt.Load()
		if lastAccess == 0 {
			lastAccess = entry.fetchedAt.Load()
		}

		age := now - lastAccess
		if age > cacheGracePeriodMs {
			entry.close()
			sc.entries.Delete(key)
		}

		return true
	})
}

// stop shuts down the shared cache.
func (sc *sharedCache) stop() {
	// Cancel context first to stop all background operations
	sc.cancel()

	close(sc.stopCleanup)

	sc.entries.Range(func(key, value any) bool {
		entry := value.(*sharedCacheEntry)
		entry.close()
		return true
	})
}
