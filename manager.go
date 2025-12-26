package pecs

import (
	"context"
	"fmt"
	"log/slog"
	"reflect"
	"sync"
	"time"
	"unsafe"

	"github.com/df-mc/dragonfly/server/player"
	"github.com/df-mc/dragonfly/server/world"
	"github.com/go-gl/mathgl/mgl64"
	"github.com/google/uuid"
)

// Manager is the central PECS coordinator.
// It manages sessions, bundles, and the scheduler.
// Multiple Manager instances can coexist in the same process for running
// multiple isolated servers.
type Manager struct {
	// registry holds component type registrations for this manager
	registry *componentRegistry

	// bundles holds all registered bundles
	bundles []*Bundle

	// handlers holds all registered handler metadata
	handlers []*handlerMeta

	// resources holds global resources
	resources   map[reflect.Type]unsafe.Pointer
	resourcesMu sync.RWMutex

	// sessions holds all active sessions
	sessions   map[*world.EntityHandle]*Session
	sessionsMu sync.RWMutex

	// sessionsByUUID provides UUID-based session lookup
	sessionsByUUID   map[uuid.UUID]*Session
	sessionsByUUIDMu sync.RWMutex

	// sessionsByName provides Name-based session lookup
	sessionsByName   map[string]*Session
	sessionsByNameMu sync.RWMutex

	// sessionsByXUID provides XUID-based session lookup
	sessionsByXUID   map[string]*Session
	sessionsByXUIDMu sync.RWMutex

	// sessionsByFakeID provides FakeMarker.ID-based session lookup for fake players
	sessionsByFakeID   map[string]*Session
	sessionsByFakeIDMu sync.RWMutex

	// sessionsByWorld groups sessions by world for optimized scheduling
	sessionsByWorld   map[*world.World]map[*Session]struct{}
	sessionsByWorldMu sync.RWMutex

	// taskQueue holds scheduled tasks
	taskQueue *taskQueue

	// scheduler manages loop and task execution
	scheduler *Scheduler

	// Federation support
	// peerCache manages cached data for remote players (Peer[T] resolution)
	peerCache *peerCache

	// sharedCache manages cached data for shared entities (Shared[T] resolution)
	sharedCache *sharedCache
}

// newManager creates a new manager.
func newManager(ws []*world.World) *Manager {
	m := &Manager{
		registry:         newComponentRegistry(),
		resources:        make(map[reflect.Type]unsafe.Pointer),
		sessions:         make(map[*world.EntityHandle]*Session),
		sessionsByUUID:   make(map[uuid.UUID]*Session),
		sessionsByName:   make(map[string]*Session),
		sessionsByXUID:   make(map[string]*Session),
		sessionsByFakeID: make(map[string]*Session),
		sessionsByWorld:  make(map[*world.World]map[*Session]struct{}),
		taskQueue:        newTaskQueue(),
	}
	m.scheduler = newScheduler(m, ws)
	m.peerCache = newPeerCache(m)
	m.sharedCache = newSharedCache(m)
	return m
}

// addResource registers a global resource.
func (m *Manager) addResource(res any) {
	t := reflect.TypeOf(res)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	m.resourcesMu.Lock()
	m.resources[t] = ptrValueRaw(res)
	m.resourcesMu.Unlock()
}

// getResource retrieves a global resource by type.
func (m *Manager) getResource(t reflect.Type) unsafe.Pointer {
	m.resourcesMu.RLock()
	defer m.resourcesMu.RUnlock()

	if ptr, ok := m.resources[t]; ok {
		return ptr
	}
	return nil
}

// ManagerResource retrieves a global resource from the manager.
// Returns nil if the resource is not found.
func ManagerResource[T any](m *Manager) *T {
	if m == nil {
		return nil
	}
	t := reflect.TypeFor[T]()
	ptr := m.getResource(t)
	if ptr == nil {
		return nil
	}
	return (*T)(ptr)
}

// Resource retrieves a global resource via the session's manager.
// Returns nil if the session or resource is not found.
func Resource[T any](s *Session) *T {
	if s == nil || s.manager == nil {
		return nil
	}
	return ManagerResource[T](s.manager)
}

// addSession registers a session with the manager.
func (m *Manager) addSession(s *Session) {
	m.sessionsMu.Lock()
	m.sessions[s.handle] = s
	m.sessionsMu.Unlock()

	m.sessionsByUUIDMu.Lock()
	m.sessionsByUUID[s.uuid] = s
	m.sessionsByUUIDMu.Unlock()

	m.sessionsByNameMu.Lock()
	m.sessionsByName[s.name] = s
	m.sessionsByNameMu.Unlock()

	if s.xuid != "" {
		m.sessionsByXUIDMu.Lock()
		m.sessionsByXUID[s.xuid] = s
		m.sessionsByXUIDMu.Unlock()
	}

	if w := s.cachedWorld(); w != nil {
		m.sessionsByWorldMu.Lock()
		if m.sessionsByWorld[w] == nil {
			m.sessionsByWorld[w] = make(map[*Session]struct{})
		}
		m.sessionsByWorld[w][s] = struct{}{}
		m.sessionsByWorldMu.Unlock()
	}
}

// MoveSession updates the session's world in the index.
func (m *Manager) MoveSession(s *Session, from, to *world.World) {
	m.sessionsByWorldMu.Lock()
	if from != nil && m.sessionsByWorld[from] != nil {
		delete(m.sessionsByWorld[from], s)
		if len(m.sessionsByWorld[from]) == 0 {
			delete(m.sessionsByWorld, from)
		}
	}
	if to != nil {
		if m.sessionsByWorld[to] == nil {
			m.sessionsByWorld[to] = make(map[*Session]struct{})
		}
		m.sessionsByWorld[to][s] = struct{}{}
	}
	m.sessionsByWorldMu.Unlock()
}

// removeSession unregisters a session from the manager.
func (m *Manager) removeSession(s *Session) {
	m.sessionsMu.Lock()
	delete(m.sessions, s.handle)
	m.sessionsMu.Unlock()

	m.sessionsByUUIDMu.Lock()
	delete(m.sessionsByUUID, s.uuid)
	m.sessionsByUUIDMu.Unlock()

	m.sessionsByNameMu.Lock()
	delete(m.sessionsByName, s.name)
	m.sessionsByNameMu.Unlock()

	if s.xuid != "" {
		m.sessionsByXUIDMu.Lock()
		delete(m.sessionsByXUID, s.xuid)
		m.sessionsByXUIDMu.Unlock()
	}

	if w := s.cachedWorld(); w != nil {
		m.sessionsByWorldMu.Lock()
		if m.sessionsByWorld[w] != nil {
			delete(m.sessionsByWorld[w], s)
			if len(m.sessionsByWorld[w]) == 0 {
				delete(m.sessionsByWorld, w)
			}
		}
		m.sessionsByWorldMu.Unlock()
	}

	// Clean up fakeID index if this was a fake player
	if s.IsFake() && s.fakeID != "" {
		m.sessionsByFakeIDMu.Lock()
		delete(m.sessionsByFakeID, s.fakeID)
		m.sessionsByFakeIDMu.Unlock()
	}

	// Clear relations pointing to this session
	m.clearAllRelationsTo(s)
}

// getSessionByHandle retrieves a session by entity handle (internal).
func (m *Manager) getSessionByHandle(h *world.EntityHandle) *Session {
	m.sessionsMu.RLock()
	defer m.sessionsMu.RUnlock()
	return m.sessions[h]
}

// GetSession retrieves the session for a player.
func (m *Manager) GetSession(p *player.Player) *Session {
	return m.getSessionByHandle(p.H())
}

// GetSessionByHandle retrieves a session by entity handle.
func (m *Manager) GetSessionByHandle(h *world.EntityHandle) *Session {
	return m.getSessionByHandle(h)
}

// GetSessionByUUID retrieves a session by UUID.
func (m *Manager) GetSessionByUUID(id uuid.UUID) *Session {
	m.sessionsByUUIDMu.RLock()
	defer m.sessionsByUUIDMu.RUnlock()
	return m.sessionsByUUID[id]
}

// GetSessionByName retrieves a session by player Name.
func (m *Manager) GetSessionByName(name string) *Session {
	m.sessionsByNameMu.RLock()
	defer m.sessionsByNameMu.RUnlock()
	return m.sessionsByName[name]
}

// getSessionByXUID retrieves a session by player XUID (internal).
func (m *Manager) getSessionByXUID(xuid string) *Session {
	m.sessionsByXUIDMu.RLock()
	defer m.sessionsByXUIDMu.RUnlock()
	return m.sessionsByXUID[xuid]
}

// GetSessionByID retrieves a session by its federated ID.
// This works for both real players (using XUID) and fake players (using FakeMarker.ID).
// Returns nil for entities (which have no federated ID).
func (m *Manager) GetSessionByID(id string) *Session {
	if id == "" {
		return nil
	}

	// Fast path: check XUID index first (real players)
	if s := m.getSessionByXUID(id); s != nil {
		return s
	}

	// Fast path: check fakeID index (fake players)
	m.sessionsByFakeIDMu.RLock()
	s := m.sessionsByFakeID[id]
	m.sessionsByFakeIDMu.RUnlock()
	return s
}

// AllSessions returns a slice of all active sessions.
func (m *Manager) AllSessions() []*Session {
	m.sessionsMu.RLock()
	defer m.sessionsMu.RUnlock()

	sessions := make([]*Session, 0, len(m.sessions))
	for _, s := range m.sessions {
		if !s.closed.Load() {
			sessions = append(sessions, s)
		}
	}
	return sessions
}

// AllSessionsInWorld returns all active sessions in the specified world.
func (m *Manager) AllSessionsInWorld(w *world.World) []*Session {
	if w == nil {
		return nil
	}

	m.sessionsByWorldMu.RLock()
	defer m.sessionsByWorldMu.RUnlock()

	set := m.sessionsByWorld[w]
	if set == nil {
		return nil
	}

	sessions := make([]*Session, 0, len(set))
	for s := range set {
		if !s.closed.Load() {
			sessions = append(sessions, s)
		}
	}
	return sessions
}

// SessionCount returns the number of active sessions.
func (m *Manager) SessionCount() int {
	m.sessionsMu.RLock()
	defer m.sessionsMu.RUnlock()
	return len(m.sessions)
}

// Broadcast dispatches an event to all active sessions.
func (m *Manager) Broadcast(event any) {
	sessions := m.AllSessions()
	for _, s := range sessions {
		s.Dispatch(event)
	}
}

// BroadcastExcept dispatches an event to all active sessions except the specified ones.
func (m *Manager) BroadcastExcept(event any, exclude ...*Session) {
	if len(exclude) == 0 {
		m.Broadcast(event)
		return
	}

	// Build exclusion set for O(1) lookup
	excludeSet := make(map[*Session]struct{}, len(exclude))
	for _, s := range exclude {
		excludeSet[s] = struct{}{}
	}

	sessions := m.AllSessions()
	for _, s := range sessions {
		if _, excluded := excludeSet[s]; !excluded {
			s.Dispatch(event)
		}
	}
}

// MessageAll sends a chat message to all online players.
func (m *Manager) MessageAll(tx *world.Tx, message string) {
	for _, s := range m.AllSessions() {
		p, _ := s.Player(tx)
		p.Message(message)
	}
}

// clearAllRelationsTo removes all relations pointing to a session.
func (m *Manager) clearAllRelationsTo(target *Session) {
	m.sessionsMu.RLock()
	sessions := make([]*Session, 0, len(m.sessions))
	for _, s := range m.sessions {
		sessions = append(sessions, s)
	}
	m.sessionsMu.RUnlock()

	for _, s := range sessions {
		s.clearRelationsTo(target)
	}
}

// groupedSessions returns a snapshot of sessions grouped by world.
func (m *Manager) groupedSessions() map[*world.World][]*Session {
	m.sessionsByWorldMu.RLock()
	defer m.sessionsByWorldMu.RUnlock()

	result := make(map[*world.World][]*Session, len(m.sessionsByWorld))
	for w, set := range m.sessionsByWorld {
		list := make([]*Session, 0, len(set))
		for s := range set {
			list = append(list, s)
		}
		result[w] = list
	}
	return result
}

// getPlayerFromTx gets a player from a transaction using the session's handle.
func (m *Manager) getPlayerFromTx(tx *world.Tx, s *Session) *player.Player {
	// Get all entities in the transaction and find the matching one
	e, ok := s.Handle().Entity(tx)
	if !ok {
		return nil
	}
	p, ok := e.(*player.Player)
	if !ok {
		return nil
	}
	return p
}

// getTaskMeta retrieves task metadata from any bundle.
func (m *Manager) getTaskMeta(t reflect.Type) *SystemMeta {
	for _, b := range m.bundles {
		if meta := b.getTaskMeta(t); meta != nil {
			return meta
		}
	}
	return nil
}

// build initializes all bundles and systems.
func (m *Manager) build() error {
	for _, b := range m.bundles {
		if err := b.build(m.registry); err != nil {
			return err
		}

		// Register handlers
		for i, reg := range b.handlers {
			if err := m.registerHandler(reg.handler, b); err != nil {
				return err
			}
			// Store computed metadata back
			if i < len(b.handlerMeta) {
				b.handlerMeta[i] = m.handlers[len(m.handlers)-1].meta
			}
		}

		// Register loops with scheduler
		for i, reg := range b.loops {
			if i < len(b.loopMeta) {
				m.scheduler.addLoop(b.loopMeta[i], b, reg.interval, reg.stage)
			}
		}
	}

	return nil
}

// Start starts the manager and scheduler.
func (m *Manager) Start() {
	m.scheduler.Start()
}

// Shutdown gracefully shuts down the manager.
func (m *Manager) Shutdown() {
	m.scheduler.Stop()

	// Stop federation caches
	if m.peerCache != nil {
		m.peerCache.stop()
	}
	if m.sharedCache != nil {
		m.sharedCache.stop()
	}

	// Close all sessions
	m.sessionsMu.Lock()
	sessions := make([]*Session, 0, len(m.sessions))
	for _, s := range m.sessions {
		sessions = append(sessions, s)
	}
	m.sessionsMu.Unlock()

	for _, s := range sessions {
		s.close()
	}
}

// RegisterPeerProvider registers a provider for Peer[T] resolution.
func (m *Manager) RegisterPeerProvider(p PeerProvider, opts ...ProviderOption) {
	options := defaultProviderOptions()
	for _, opt := range opts {
		opt(&options)
	}
	m.peerCache.registerProvider(p, options)
}

// RegisterSharedProvider registers a provider for Shared[T] resolution.
func (m *Manager) RegisterSharedProvider(p SharedProvider, opts ...ProviderOption) {
	options := defaultProviderOptions()
	for _, opt := range opts {
		opt(&options)
	}
	m.sharedCache.registerProvider(p, options)
}

// spawnActor creates a fake player entity and registers it as a PECS session.
// This is the internal helper used by SpawnFake and SpawnEntity.
// Unlike NewSession, this does not initialize from providers (bots don't have XUID).
func (m *Manager) spawnActor(tx *world.Tx, cfg ActorConfig) *Session {
	// Create player entity spawn options
	opts := world.EntitySpawnOpts{Position: cfg.Position}

	// Create the entity handle with player type
	ent := opts.New(player.Type, player.Config{
		Name:     cfg.Name,
		Skin:     cfg.Skin,
		Position: cfg.Position,
	})

	// Add to world and get the player
	e := tx.AddEntity(ent)
	p := e.(*player.Player)

	// Apply rotation
	p.Move(mgl64.Vec3{}, cfg.Yaw, cfg.Pitch)

	// Create session manually (no provider init for bots)
	s := &Session{
		handle:  p.H(),
		uuid:    p.UUID(),
		name:    p.Name(),
		xuid:    "", // Bots have no XUID
		manager: m,
	}

	s.updateWorldCache(tx.World())
	m.addSession(s)

	// Set the PECS handler on the player
	p.Handle(NewHandler(s, p))

	return s
}

// SpawnFake creates a fake player (testing bot) and registers it as a PECS session.
// The fakeID is used as the federated identifier for Peer[T] resolution.
// Returns the session for adding components.
func (m *Manager) SpawnFake(tx *world.Tx, cfg ActorConfig, fakeID string) *Session {
	sess := m.spawnActor(tx, cfg)
	sess.isFake = true
	sess.fakeID = fakeID
	Add(sess, &FakeMarker{})

	// Register in fakeID index for fast lookup
	if fakeID != "" {
		m.sessionsByFakeIDMu.Lock()
		m.sessionsByFakeID[fakeID] = sess
		m.sessionsByFakeIDMu.Unlock()
	}

	return sess
}

// SpawnEntity creates an NPC entity and registers it as a PECS session.
// Entities do not participate in cross-server Peer[T] lookups.
// Returns the session for adding components.
func (m *Manager) SpawnEntity(tx *world.Tx, cfg ActorConfig) *Session {
	sess := m.spawnActor(tx, cfg)
	sess.isEntity = true
	Add(sess, &EntityMarker{})
	return sess
}

// ResolvePeer resolves a Peer[T] reference to the target's component.
// If the player is local, returns their component directly.
// If remote, fetches and caches via the registered PeerProvider.
func (m *Manager) ResolvePeer(playerID string, componentType reflect.Type) unsafe.Pointer {
	if playerID == "" {
		return nil
	}

	// Fast path: check if player is local (works for real and fake players)
	if session := m.GetSessionByID(playerID); session != nil && !session.closed.Load() {
		// Get component ID for this type
		compID, ok := m.registry.getID(componentType)
		if !ok {
			return nil
		}

		session.mu.RLock()
		ptr := session.getComponentUnsafe(compID)
		session.mu.RUnlock()
		return ptr
	}

	// Slow path: resolve via peer cache
	return m.peerCache.resolve(playerID, componentType)
}

// ResolvePeers resolves multiple Peer[T] references.
// Uses batch fetching for efficiency when multiple players are remote.
func (m *Manager) ResolvePeers(playerIDs []string, componentType reflect.Type) []unsafe.Pointer {
	if len(playerIDs) == 0 {
		return nil
	}

	results := make([]unsafe.Pointer, len(playerIDs))
	var remoteIDs []string
	var remoteIndices []int

	// First pass: resolve local sessions
	compID, hasCompID := m.registry.getID(componentType)

	for i, id := range playerIDs {
		if id == "" {
			continue
		}

		if session := m.GetSessionByID(id); session != nil && !session.closed.Load() {
			if hasCompID {
				session.mu.RLock()
				results[i] = session.getComponentUnsafe(compID)
				session.mu.RUnlock()
			}
		} else {
			remoteIDs = append(remoteIDs, id)
			remoteIndices = append(remoteIndices, i)
		}
	}

	// Batch resolve remote players
	if len(remoteIDs) > 0 {
		remoteResults := m.peerCache.resolveMany(remoteIDs, componentType)
		for i, ptr := range remoteResults {
			results[remoteIndices[i]] = ptr
		}
	}

	return results
}

// ResolveShared resolves a Shared[T] reference to the entity's data.
func (m *Manager) ResolveShared(entityID string, dataType reflect.Type) unsafe.Pointer {
	if entityID == "" {
		return nil
	}
	return m.sharedCache.resolve(entityID, dataType)
}

// ResolveSharedMany resolves multiple Shared[T] references.
func (m *Manager) ResolveSharedMany(entityIDs []string, dataType reflect.Type) []unsafe.Pointer {
	if len(entityIDs) == 0 {
		return nil
	}
	return m.sharedCache.resolveMany(entityIDs, dataType)
}

// TickNumber returns the current scheduler tick number.
func (m *Manager) TickNumber() uint64 {
	if m.scheduler == nil {
		return 0
	}
	return m.scheduler.tickNumber
}

// NewSession creates a new session for a player.
// This should be called when a player joins and the returned session
// should be passed to player.Handle() wrapped with NewHandler().
// Automatically fetches data from registered PeerProviders and subscribes to updates.
func (m *Manager) NewSession(p *player.Player) (*Session, error) {
	s := &Session{
		handle:  p.H(),
		uuid:    p.UUID(),
		name:    p.Name(),
		xuid:    p.XUID(),
		manager: m,
	}

	// Auto-populate from registered providers
	if err := m.initSessionFromProviders(s); err != nil {
		return nil, err
	}

	s.updateWorldCache(p.Tx().World())
	m.addSession(s)

	return s, nil
}

// initSessionFromProviders fetches initial data from all registered providers
// and subscribes to updates for the local player.
func (m *Manager) initSessionFromProviders(s *Session) error {
	playerID := s.xuid
	if playerID == "" {
		// Not an error - just skip provider initialization
		// Player can still play, just without provider-backed components
		slog.Debug("pecs: skipping provider init - no XUID", "player", s.name)
		return nil
	}

	providers := m.peerCache.getAllProviders()
	if len(providers) == 0 {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	for _, entry := range providers {
		provider := entry.provider
		providerName := provider.Name()

		// Fetch initial data
		components, err := provider.FetchPlayer(ctx, playerID)
		if err != nil {
			if entry.options.Required {
				return fmt.Errorf("required provider %s failed: %w", providerName, err)
			}
			slog.Warn("pecs: optional provider failed",
				"provider", providerName,
				"player", s.name,
				"error", err)
			continue
		}

		if len(components) == 0 {
			continue
		}

		// Add fetched components to session
		for _, comp := range components {
			if comp == nil {
				continue
			}
			m.addComponentToSession(s, comp)
		}

		// Subscribe to updates
		updates := make(chan PlayerUpdate, 16)
		sub, err := provider.SubscribePlayer(context.Background(), playerID, updates)
		if err != nil {
			if entry.options.Required {
				return fmt.Errorf("required provider %s subscription failed: %w", providerName, err)
			}
			slog.Warn("pecs: optional provider subscription failed",
				"provider", providerName,
				"player", s.name,
				"error", err)
			close(updates)
			continue
		}

		s.addProviderSub(sub)
		go m.processProviderUpdates(s, updates)
	}

	return nil
}

// addComponentToSession adds a component of any type to the session using reflection.
func (m *Manager) addComponentToSession(s *Session, component any) {
	val := reflect.ValueOf(component)
	if val.Kind() != reflect.Ptr {
		return
	}

	t := val.Type().Elem()
	id := m.registry.register(t)

	s.mu.Lock()
	oldPtr := s.components[id]
	if oldPtr != nil {
		// Update in place
		size := t.Size()
		oldBytes := unsafe.Slice((*byte)(oldPtr), size)
		newBytes := unsafe.Slice((*byte)(val.UnsafePointer()), size)
		copy(oldBytes, newBytes)
		s.mu.Unlock()
		return
	}

	s.components[id] = val.UnsafePointer()
	s.mask.Set(id)
	s.mu.Unlock()
}

// processProviderUpdates handles real-time updates from a provider subscription.
func (m *Manager) processProviderUpdates(s *Session, updates <-chan PlayerUpdate) {
	for update := range updates {
		if s.closed.Load() {
			return
		}
		if update.Data == nil {
			// Component removal - not implemented yet
			continue
		}
		m.addComponentToSession(s, update.Data)
	}
}
