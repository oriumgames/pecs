package pecs

import (
	"reflect"
	"sync"
	"unsafe"

	"github.com/df-mc/dragonfly/server/player"
	"github.com/df-mc/dragonfly/server/world"
	"github.com/google/uuid"
)

// Manager is the central PECS coordinator.
// It manages sessions, bundles, and the scheduler.
type Manager struct {
	// bundles holds all registered bundles
	bundles []*Bundle

	// handlers holds all registered handler metadata
	handlers []*handlerMeta

	// injections holds global injections
	injections   map[reflect.Type]unsafe.Pointer
	injectionsMu sync.RWMutex

	// sessions holds all active sessions
	sessions   map[*world.EntityHandle]*Session
	sessionsMu sync.RWMutex

	// sessionsByUUID provides UUID-based session lookup
	sessionsByUUID   map[uuid.UUID]*Session
	sessionsByUUIDMu sync.RWMutex

	// sessionsByName provides Name-based session lookup
	sessionsByName   map[string]*Session
	sessionsByNameMu sync.RWMutex

	// sessionsByWorld groups sessions by world for optimized scheduling
	sessionsByWorld   map[*world.World]map[*Session]struct{}
	sessionsByWorldMu sync.RWMutex

	// taskQueue holds scheduled tasks
	taskQueue *taskQueue

	// scheduler manages loop and task execution
	scheduler *Scheduler
}

// globalManager is the singleton manager instance.
var globalManager *Manager

// newManager creates a new manager.
func newManager() *Manager {
	m := &Manager{
		injections:      make(map[reflect.Type]unsafe.Pointer),
		sessions:        make(map[*world.EntityHandle]*Session),
		sessionsByUUID:  make(map[uuid.UUID]*Session),
		sessionsByName:  make(map[string]*Session),
		sessionsByWorld: make(map[*world.World]map[*Session]struct{}),
		taskQueue:       newTaskQueue(),
	}
	m.scheduler = newScheduler(m)
	return m
}

// addInjection registers a global injection.
func (m *Manager) addInjection(inj any) {
	t := reflect.TypeOf(inj)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	m.injectionsMu.Lock()
	m.injections[t] = unsafe.Pointer(reflect.ValueOf(inj).Pointer())
	m.injectionsMu.Unlock()
}

// getInjection retrieves a global injection by type.
func (m *Manager) getInjection(t reflect.Type) unsafe.Pointer {
	m.injectionsMu.RLock()
	defer m.injectionsMu.RUnlock()

	if ptr, ok := m.injections[t]; ok {
		return ptr
	}

	// Also check bundle injections
	for _, b := range m.bundles {
		if ptr := b.getInjection(t); ptr != nil {
			return ptr
		}
	}

	return nil
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

	// Clear relations pointing to this session
	m.clearAllRelationsTo(s)
}

// getSessionByHandle retrieves a session by entity handle.
func (m *Manager) getSessionByHandle(h *world.EntityHandle) *Session {
	m.sessionsMu.RLock()
	defer m.sessionsMu.RUnlock()
	return m.sessions[h]
}

// getSessionByUUID retrieves a session by UUID.
func (m *Manager) getSessionByUUID(id uuid.UUID) *Session {
	m.sessionsByUUIDMu.RLock()
	defer m.sessionsByUUIDMu.RUnlock()
	return m.sessionsByUUID[id]
}

// getSessionByName retrieves a session by player Name.
func (m *Manager) getSessionByName(name string) *Session {
	m.sessionsByNameMu.RLock()
	defer m.sessionsByNameMu.RUnlock()
	return m.sessionsByName[name]
}

// allSessions returns a slice of all active sessions.
func (m *Manager) allSessions() []*Session {
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

// SessionCount returns the number of active sessions.
func (m *Manager) SessionCount() int {
	m.sessionsMu.RLock()
	defer m.sessionsMu.RUnlock()
	return len(m.sessions)
}

// Broadcast dispatches an event to all active sessions.
func (m *Manager) Broadcast(event any) {
	sessions := m.allSessions()
	for _, s := range sessions {
		s.Dispatch(event)
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

// GetSessionByName retrieves a session by the player's name.
func GetSessionByName(name string) *Session {
	if globalManager == nil {
		return nil
	}
	return globalManager.getSessionByName(name)
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
		if err := b.build(); err != nil {
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

// Global returns the global manager instance.
func Global() *Manager {
	return globalManager
}

// Broadcast dispatches an event to all active sessions via the global manager.
func Broadcast(event any) {
	if globalManager != nil {
		globalManager.Broadcast(event)
	}
}

// AllSessions returns a slice of all active sessions.
// This is useful for broadcasting or iterating all players.
func AllSessions() []*Session {
	if globalManager == nil {
		return nil
	}
	return globalManager.allSessions()
}

// SessionCount returns the number of active sessions.
func SessionCount() int {
	if globalManager == nil {
		return 0
	}
	return globalManager.SessionCount()
}

// TickNumber returns the current scheduler tick number.
func TickNumber() uint64 {
	if globalManager == nil || globalManager.scheduler == nil {
		return 0
	}
	return globalManager.scheduler.tickNumber
}
