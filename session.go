package pecs

import (
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/df-mc/dragonfly/server/player"
	"github.com/df-mc/dragonfly/server/world"
	"github.com/google/uuid"
)

// Session represents a player's session in PECS.
// It wraps the player's EntityHandle (which is persistent across transactions)
// and stores all components attached to the player.
//
// Sessions are created when players join and destroyed when they leave.
// They implement player.Handler to intercept all player events.
type Session struct {
	// handle is the persistent entity handle for the player
	handle *world.EntityHandle

	// uuid is cached for fast lookup
	uuid uuid.UUID

	// name is cached for fast lookup
	name string

	// xuid is cached for fast lookup
	xuid string

	// mask tracks which components are present (256 bits)
	mask Bitmask

	// worldCache is the atomic pointer to the player's world
	worldCache unsafe.Pointer

	// components stores component pointers indexed by ComponentID
	components [MaxComponents]unsafe.Pointer

	// mu protects mask and components
	mu sync.RWMutex

	// manager is the PECS manager that owns this session
	manager *Manager

	// closed indicates if the session has been closed
	closed atomic.Bool

	// pendingTasks holds scheduled tasks for this session
	pendingTasks []*scheduledTask
	taskMu       sync.Mutex
}

// NewSession creates a new session for a player.
// This should be called when a player joins and the returned session
// should be passed to player.Handle().
func NewSession(p *player.Player) *Session {
	s := &Session{
		handle: p.H(),
		uuid:   p.UUID(),
		name:   p.Name(),
		xuid:   p.XUID(),
	}

	s.updateWorldCache(p.Tx().World())

	// Register with global manager if initialized
	if globalManager != nil {
		s.manager = globalManager
		globalManager.addSession(s)
	}

	return s
}

// Handle returns the underlying EntityHandle.
func (s *Session) Handle() *world.EntityHandle {
	return s.handle
}

// UUID returns the player's UUID.
func (s *Session) UUID() uuid.UUID {
	return s.uuid
}

// Name returns the player's name.
func (s *Session) Name() string {
	return s.name
}

// XUID returns the player's XUID.
func (s *Session) XUID() string {
	return s.xuid
}

// Player retrieves the *player.Player instance associated with this session within the given transaction.
// It returns (nil, false) if the player entity is not present in the transaction (e.g. offline or in another world).
//
// Usage:
//
//	if p, ok := s.Player(tx); ok {
//	    p.Message("Hello!")
//	}
func (s *Session) Player(tx *world.Tx) (*player.Player, bool) {
	e, ok := s.handle.Entity(tx)
	if !ok {
		return nil, false
	}
	return e.(*player.Player), true
}

// Exec runs a function within the session's world transaction.
// Returns false if the player is offline or the session is closed.
func (s *Session) Exec(fn func(tx *world.Tx, p *player.Player)) bool {
	if s.closed.Load() {
		return false
	}

	return s.handle.ExecWorld(func(tx *world.Tx, e world.Entity) {
		p, ok := e.(*player.Player)
		if !ok {
			return
		}
		fn(tx, p)
	})
}

// World returns the world the player is currently in.
// Returns the cached world (may be slightly stale).
func (s *Session) World() *world.World {
	return s.cachedWorld()
}

// Manager returns the PECS manager for this session.
func (s *Session) Manager() *Manager {
	return s.manager
}

// Closed returns true if the session has been closed.
func (s *Session) Closed() bool {
	return s.closed.Load()
}

// Mask returns a copy of the session's component bitmask.
// This is primarily for debugging and testing.
func (s *Session) Mask() Bitmask {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.mask.Clone()
}

// updateWorldCache updates the cached world pointer atomically.
func (s *Session) updateWorldCache(w *world.World) {
	atomic.StorePointer(&s.worldCache, unsafe.Pointer(w))
}

// cachedWorld returns the cached world pointer.
func (s *Session) cachedWorld() *world.World {
	return (*world.World)(atomic.LoadPointer(&s.worldCache))
}

// String returns a string representation of the session for debugging.
func (s *Session) String() string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	comps := ""
	for id := range ComponentID(MaxComponents) {
		if s.mask.Has(id) {
			if comps != "" {
				comps += ", "
			}
			comps += ComponentName(id)
		}
	}

	return "Session{Name: " + s.name + ", XUID: " + s.xuid + ", UUID: " + s.uuid.String() + ", Handle: " + fmt.Sprintf("%p", s.handle) + ", Components: [" + comps + "]}"
}

// canRun checks if the session passes the bitmask filter for a system.
func (s *Session) canRun(meta *SystemMeta) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.canRunUnsafe(meta)
}

// canRunUnsafe checks bitmask without locking. Caller must hold lock.
func (s *Session) canRunUnsafe(meta *SystemMeta) bool {
	// Check required components
	if !s.mask.ContainsAll(meta.RequireMask) {
		return false
	}
	// Check excluded components
	if s.mask.ContainsAny(meta.ExcludeMask) {
		return false
	}
	return true
}

// close closes the session and cleans up all resources.
// This is called automatically when the player disconnects.
func (s *Session) close() {
	if s.closed.Swap(true) {
		return // Already closed
	}

	// Cancel all pending tasks
	s.taskMu.Lock()
	tasks := s.pendingTasks
	s.pendingTasks = nil
	s.taskMu.Unlock()

	for _, task := range tasks {
		task.cancelled.Store(true)
	}

	// Collect components that need Detach called
	type detachInfo struct {
		component Detachable
	}
	var toDetach []detachInfo

	s.mu.Lock()
	for id := range ComponentID(MaxComponents) {
		ptr := s.components[id]
		if ptr == nil {
			continue
		}

		s.components[id] = nil
		s.mask.Clear(id)

		// Check if component implements Detachable
		t := ComponentType(id)
		if t != nil {
			// Create a pointer to the component and check interface
			val := reflect.NewAt(t, ptr).Interface()
			if d, ok := val.(Detachable); ok {
				toDetach = append(toDetach, detachInfo{component: d})
			}
		}
	}
	s.mu.Unlock()

	// Call Detach outside the lock
	for _, info := range toDetach {
		info.component.Detach(s)
	}

	// Disconnect the player if still online
	if s.handle != nil {
		s.Exec(func(tx *world.Tx, p *player.Player) {
			p.Disconnect("Server shutting down")
		})
	}

	// Notify manager
	if s.manager != nil {
		s.manager.removeSession(s)
	}
}

// clearRelationsTo removes all relations pointing to the target session.
// This is called when a session closes.
func (s *Session) clearRelationsTo(target *Session) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for id := range ComponentID(MaxComponents) {
		ptr := s.components[id]
		if ptr != nil {
			// Use the generic helper which handles both interface check and reflection fallback
			clearRelationsTo(ptr, target)
		}
	}
}

// addTask adds a scheduled task to this session.
func (s *Session) addTask(task *scheduledTask) {
	s.taskMu.Lock()
	s.pendingTasks = append(s.pendingTasks, task)
	s.taskMu.Unlock()
}

// removeTask removes a scheduled task from this session.
func (s *Session) removeTask(task *scheduledTask) {
	s.taskMu.Lock()
	for i, t := range s.pendingTasks {
		if t == task {
			s.pendingTasks = append(s.pendingTasks[:i], s.pendingTasks[i+1:]...)
			break
		}
	}
	s.taskMu.Unlock()
}

// GetSession retrieves the session for a player.
// Returns nil if the player doesn't have a PECS session.
func GetSession(p *player.Player) *Session {
	if globalManager == nil {
		return nil
	}
	return globalManager.getSessionByHandle(p.H())
}

// GetSessionByHandle retrieves a session by its entity handle.
func GetSessionByHandle(h *world.EntityHandle) *Session {
	if globalManager == nil {
		return nil
	}
	return globalManager.getSessionByHandle(h)
}

// GetSessionByUUID retrieves a session by the player's UUID.
func GetSessionByUUID(id uuid.UUID) *Session {
	if globalManager == nil {
		return nil
	}
	return globalManager.getSessionByUUID(id)
}
