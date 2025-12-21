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

	// providerIndex maps component type -> []PlayerProvider
	providerIndex   map[reflect.Type][]PlayerProvider
	providerIndexMu sync.RWMutex

	// providers holds all registered player providers
	providers   []playerProviderEntry
	providersMu sync.RWMutex

	// cleanupInterval is how often to run cache cleanup
	cleanupInterval time.Duration
	stopCleanup     chan struct{}
}

type playerProviderEntry struct {
	provider PlayerProvider
	options  ProviderOptions
}

// peerCacheEntry holds cached data for a single remote player.
type peerCacheEntry struct {
	playerID string
	cache    *peerCache

	// components maps component type -> unsafe.Pointer to data
	components sync.Map

	// refCount tracks how many local references point to this player
	refCount atomic.Int32

	// fetchedAt is when the data was last fetched (unix millis)
	fetchedAt atomic.Int64

	// status: 0=pending, 1=ready, 2=error, 3=closing
	status atomic.Uint32

	// subscriptions holds active provider subscriptions
	subscriptions   []Subscription
	subscriptionsMu sync.Mutex

	// updateCh receives updates from providers
	updateCh chan PlayerUpdate

	// mu protects subscription setup
	mu sync.Mutex
}

const (
	peerStatusPending = iota
	peerStatusReady
	peerStatusError
	peerStatusClosing
)

// newPeerCache creates a new peer cache.
func newPeerCache(manager *Manager) *peerCache {
	pc := &peerCache{
		manager:         manager,
		providerIndex:   make(map[reflect.Type][]PlayerProvider),
		cleanupInterval: 10 * time.Second,
		stopCleanup:     make(chan struct{}),
	}
	go pc.cleanupLoop()
	return pc
}

// registerProvider adds a player provider to the cache.
func (pc *peerCache) registerProvider(p PlayerProvider, opts ProviderOptions) {
	pc.providersMu.Lock()
	pc.providers = append(pc.providers, playerProviderEntry{p, opts})
	pc.providersMu.Unlock()

	// Index by component type
	pc.providerIndexMu.Lock()
	for _, t := range p.PlayerComponents() {
		pc.providerIndex[t] = append(pc.providerIndex[t], p)
	}
	pc.providerIndexMu.Unlock()
}

// getProviders returns providers that handle the given component type.
func (pc *peerCache) getProviders(componentType reflect.Type) []PlayerProvider {
	pc.providerIndexMu.RLock()
	defer pc.providerIndexMu.RUnlock()
	return pc.providerIndex[componentType]
}

// resolve gets or creates a cache entry for a player and returns their component.
// Returns nil if the player doesn't have the component or resolution fails.
func (pc *peerCache) resolve(playerID string, componentType reflect.Type) unsafe.Pointer {
	if playerID == "" {
		return nil
	}

	// Get or create entry
	entry := pc.getOrCreateEntry(playerID)
	entry.refCount.Add(1)

	// Wait for ready state if pending
	if entry.status.Load() == peerStatusPending {
		entry.mu.Lock()
		if entry.status.Load() == peerStatusPending {
			pc.fetchAndSubscribe(entry, componentType)
		}
		entry.mu.Unlock()
	}

	// Check if ready
	if entry.status.Load() != peerStatusReady {
		entry.refCount.Add(-1)
		return nil
	}

	// Get component
	if val, ok := entry.components.Load(componentType); ok {
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
		entry.refCount.Add(1)
		entries[i] = entry

		switch entry.status.Load() {
		case peerStatusReady:
			if val, ok := entry.components.Load(componentType); ok {
				results[i] = val.(unsafe.Pointer)
			}
		case peerStatusPending:
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
		updateCh: make(chan PlayerUpdate, 16),
	}

	actual, loaded := pc.entries.LoadOrStore(playerID, entry)
	if loaded {
		return actual.(*peerCacheEntry)
	}

	// Start update processor
	go entry.processUpdates()

	return entry
}

// fetchAndSubscribe fetches initial data and sets up subscriptions.
func (pc *peerCache) fetchAndSubscribe(entry *peerCacheEntry, componentType reflect.Type) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	providers := pc.getProviders(componentType)
	if len(providers) == 0 {
		entry.status.Store(peerStatusError)
		return
	}

	var wg sync.WaitGroup
	var anySuccess atomic.Bool

	for _, p := range providers {
		wg.Add(1)
		go func(provider PlayerProvider) {
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
				entry.components.Store(t, unsafe.Pointer(reflect.ValueOf(comp).Pointer()))
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
		entry.status.Store(peerStatusReady)
	} else {
		entry.status.Store(peerStatusError)
	}
}

// batchFetch fetches multiple players at once.
func (pc *peerCache) batchFetch(playerIDs []string, indices []int, entries []*peerCacheEntry, componentType reflect.Type, results []unsafe.Pointer) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
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

			if entry.status.Load() != peerStatusPending {
				continue
			}

			components, ok := componentsMap[id]
			if !ok || len(components) == 0 {
				entry.status.Store(peerStatusError)
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
				entry.components.Store(t, unsafe.Pointer(reflect.ValueOf(comp).Pointer()))

				if t == componentType {
					results[idx] = unsafe.Pointer(reflect.ValueOf(comp).Pointer())
				}
			}

			entry.fetchedAt.Store(now)
			entry.status.Store(peerStatusReady)

			// Subscribe for updates (async)
			go pc.subscribeEntry(entry, p)
		}

		break // Only use first provider
	}
}

// subscribeEntry sets up subscription for an entry.
func (pc *peerCache) subscribeEntry(entry *peerCacheEntry, provider PlayerProvider) {
	ctx := context.Background()
	sub, err := provider.SubscribePlayer(ctx, entry.playerID, entry.updateCh)
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
		if e.status.Load() == peerStatusClosing {
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
			e.components.Store(t, unsafe.Pointer(reflect.ValueOf(update.Data).Pointer()))
		}

		e.fetchedAt.Store(time.Now().UnixMilli())
	}
}

// release decrements the reference count.
func (e *peerCacheEntry) release() {
	e.refCount.Add(-1)
}

// close shuts down the entry and cleans up resources.
func (e *peerCacheEntry) close() {
	if !e.status.CompareAndSwap(peerStatusReady, peerStatusClosing) &&
		!e.status.CompareAndSwap(peerStatusPending, peerStatusClosing) &&
		!e.status.CompareAndSwap(peerStatusError, peerStatusClosing) {
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

// cleanup removes entries with zero references past their grace period.
func (pc *peerCache) cleanup() {
	now := time.Now().UnixMilli()

	pc.entries.Range(func(key, value any) bool {
		entry := value.(*peerCacheEntry)

		if entry.refCount.Load() <= 0 {
			// Check grace period (default 30 seconds)
			age := now - entry.fetchedAt.Load()
			if age > 30_000 {
				entry.close()
				pc.entries.Delete(key)
			}
		}

		return true
	})
}

// stop shuts down the peer cache.
func (pc *peerCache) stop() {
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

	// providerIndex maps data type -> []EntityProvider
	providerIndex   map[reflect.Type][]EntityProvider
	providerIndexMu sync.RWMutex

	// providers holds all registered entity providers
	providers   []entityProviderEntry
	providersMu sync.RWMutex

	// cleanupInterval is how often to run cache cleanup
	cleanupInterval time.Duration
	stopCleanup     chan struct{}
}

type entityProviderEntry struct {
	provider EntityProvider
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

	// refCount tracks how many local references point to this entity
	refCount atomic.Int32

	// fetchedAt is when the data was last fetched (unix millis)
	fetchedAt atomic.Int64

	// status: 0=pending, 1=ready, 2=error, 3=closing
	status atomic.Uint32

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
	sc := &sharedCache{
		manager:         manager,
		providerIndex:   make(map[reflect.Type][]EntityProvider),
		cleanupInterval: 10 * time.Second,
		stopCleanup:     make(chan struct{}),
	}
	go sc.cleanupLoop()
	return sc
}

// registerProvider adds an entity provider to the cache.
func (sc *sharedCache) registerProvider(p EntityProvider, opts ProviderOptions) {
	sc.providersMu.Lock()
	sc.providers = append(sc.providers, entityProviderEntry{p, opts})
	sc.providersMu.Unlock()

	// Index by data type
	sc.providerIndexMu.Lock()
	for _, t := range p.EntityComponents() {
		sc.providerIndex[t] = append(sc.providerIndex[t], p)
	}
	sc.providerIndexMu.Unlock()
}

// getProviders returns providers that handle the given data type.
func (sc *sharedCache) getProviders(dataType reflect.Type) []EntityProvider {
	sc.providerIndexMu.RLock()
	defer sc.providerIndexMu.RUnlock()
	return sc.providerIndex[dataType]
}

// resolve gets or creates a cache entry for an entity and returns its data.
func (sc *sharedCache) resolve(entityID string, dataType reflect.Type) unsafe.Pointer {
	if entityID == "" {
		return nil
	}

	// Get or create entry
	entry := sc.getOrCreateEntry(entityID, dataType)
	entry.refCount.Add(1)

	// Wait for ready state if pending
	if entry.status.Load() == peerStatusPending {
		entry.mu.Lock()
		if entry.status.Load() == peerStatusPending {
			sc.fetchAndSubscribe(entry, dataType)
		}
		entry.mu.Unlock()
	}

	// Check if ready
	if entry.status.Load() != peerStatusReady {
		entry.refCount.Add(-1)
		return nil
	}

	// Get data
	dataPtr := entry.data.Load()
	if dataPtr == nil {
		return nil
	}
	return unsafe.Pointer(reflect.ValueOf(*dataPtr).Pointer())
}

// resolveMany resolves multiple entity IDs and returns their data.
func (sc *sharedCache) resolveMany(entityIDs []string, dataType reflect.Type) []unsafe.Pointer {
	if len(entityIDs) == 0 {
		return nil
	}

	results := make([]unsafe.Pointer, len(entityIDs))

	for i, id := range entityIDs {
		if id == "" {
			continue
		}
		results[i] = sc.resolve(id, dataType)
	}

	return results
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
		updateCh: make(chan any, 16),
	}

	actual, loaded := sc.entries.LoadOrStore(key, entry)
	if loaded {
		return actual.(*sharedCacheEntry)
	}

	// Start update processor
	go entry.processUpdates()

	return entry
}

// fetchAndSubscribe fetches initial data and sets up subscription.
func (sc *sharedCache) fetchAndSubscribe(entry *sharedCacheEntry, dataType reflect.Type) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	providers := sc.getProviders(dataType)
	if len(providers) == 0 {
		entry.status.Store(peerStatusError)
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
		entry.status.Store(peerStatusReady)

		// Subscribe for updates
		go func(provider EntityProvider) {
			sub, err := provider.SubscribeEntity(context.Background(), entry.entityID, entry.updateCh)
			if err != nil {
				return
			}

			entry.subscriptionMu.Lock()
			entry.subscription = sub
			entry.subscriptionMu.Unlock()
		}(p)

		return
	}

	entry.status.Store(peerStatusError)
}

// processUpdates handles incoming updates for a cache entry.
func (e *sharedCacheEntry) processUpdates() {
	for update := range e.updateCh {
		if e.status.Load() == peerStatusClosing {
			return
		}

		if update == nil {
			// Entity deleted
			e.data.Store(nil)
			e.status.Store(peerStatusError)
		} else {
			// Update data
			e.data.Store(&update)
			e.fetchedAt.Store(time.Now().UnixMilli())
		}
	}
}

// release decrements the reference count.
func (e *sharedCacheEntry) release() {
	e.refCount.Add(-1)
}

// close shuts down the entry and cleans up resources.
func (e *sharedCacheEntry) close() {
	if !e.status.CompareAndSwap(peerStatusReady, peerStatusClosing) &&
		!e.status.CompareAndSwap(peerStatusPending, peerStatusClosing) &&
		!e.status.CompareAndSwap(peerStatusError, peerStatusClosing) {
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

// cleanup removes entries with zero references past their grace period.
func (sc *sharedCache) cleanup() {
	now := time.Now().UnixMilli()

	sc.entries.Range(func(key, value any) bool {
		entry := value.(*sharedCacheEntry)

		if entry.refCount.Load() <= 0 {
			// Check grace period (default 30 seconds)
			age := now - entry.fetchedAt.Load()
			if age > 30_000 {
				entry.close()
				sc.entries.Delete(key)
			}
		}

		return true
	})
}

// stop shuts down the shared cache.
func (sc *sharedCache) stop() {
	close(sc.stopCleanup)

	sc.entries.Range(func(key, value any) bool {
		entry := value.(*sharedCacheEntry)
		entry.close()
		return true
	})
}
