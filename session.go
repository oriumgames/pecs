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
// They implement pecs.Handler to intercept all player events.
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

	// providerSubs holds active provider subscriptions for auto-updates
	providerSubs   []Subscription
	providerSubsMu sync.Mutex

	// expirations tracks component expiration times (ComponentID -> unix millis)
	expirations  map[ComponentID]int64
	expirationMu sync.Mutex

	// isFake is cached for fast lookup (set once at spawn, immutable)
	isFake bool

	// isEntity is cached for fast lookup (set once at spawn, immutable)
	isEntity bool

	// fakeID caches the federated ID for fake players (set once at spawn)
	fakeID string
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

// ID returns the federated identifier for this session.
// For real players, this is their XUID.
// For fake players, this is their FakeMarker.ID.
// For entities, this returns an empty string (no federated ID).
// This is used for Peer[T] resolution to identify players across servers.
func (s *Session) ID() string {
	// Real player: return XUID
	if s.xuid != "" {
		return s.xuid
	}
	// Fake player: return cached fakeID
	return s.fakeID
}

// IsFake returns true if this session is a fake player (testing bot).
func (s *Session) IsFake() bool {
	return s.isFake
}

// IsEntity returns true if this session is an NPC entity.
func (s *Session) IsEntity() bool {
	return s.isEntity
}

// IsActor returns true if this session is an actor (fake player or entity).
// Bots have session.Nop as their network session.
func (s *Session) IsActor() bool {
	return s.IsFake() || s.IsEntity()
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

// Broadcast dispatches an event to all active sessions.
// This is a convenience method that delegates to Manager.Broadcast.
func (s *Session) Broadcast(event any) {
	if s.manager != nil {
		s.manager.Broadcast(event)
	}
}

// BroadcastExcept dispatches an event to all active sessions except the specified ones.
// This is a convenience method that delegates to Manager.BroadcastExcept.
func (s *Session) BroadcastExcept(event any, exclude ...*Session) {
	if s.manager != nil {
		s.manager.BroadcastExcept(event, exclude...)
	}
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
			comps += s.manager.registry.getName(id)
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

	// Close all provider subscriptions
	s.providerSubsMu.Lock()
	subs := s.providerSubs
	s.providerSubs = nil
	s.providerSubsMu.Unlock()

	for _, sub := range subs {
		if sub != nil {
			_ = sub.Close()
		}
	}

	// Cancel all pending tasks
	s.taskMu.Lock()
	tasks := s.pendingTasks
	s.pendingTasks = nil
	s.taskMu.Unlock()

	for _, task := range tasks {
		task.cancelled.Store(true)
	}

	// Collect components that need Detach called, while session is still intact.
	type detachInfo struct {
		component Detachable
	}
	var toDetach []detachInfo

	s.mu.RLock()
	for id := range ComponentID(MaxComponents) {
		ptr := s.components[id]
		if ptr == nil {
			continue
		}

		// Check if component implements Detachable
		t := s.manager.registry.getType(id)
		if t != nil {
			val := reflect.NewAt(t, ptr).Interface()
			if d, ok := val.(Detachable); ok {
				toDetach = append(toDetach, detachInfo{component: d})
			}
		}
	}
	s.mu.RUnlock()

	// Call Detach outside any session lock.
	// The session is still fully readable for these hooks.
	for _, info := range toDetach {
		info.component.Detach(s)
	}

	// Now, clear all component data from the session.
	s.mu.Lock()
	for id := range ComponentID(MaxComponents) {
		s.components[id] = nil
	}
	s.mask = Bitmask{} // Zero out the bitmask
	s.mu.Unlock()

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
			// Get the type to create a properly typed pointer for reflection
			t := s.manager.registry.getType(id)
			if t != nil {
				comp := reflect.NewAt(t, ptr).Interface()
				clearRelationsTo(comp, target)
			}
		}
	}
}

// addTask adds a scheduled task to this session.
func (s *Session) addTask(task *scheduledTask) {
	s.taskMu.Lock()
	s.pendingTasks = append(s.pendingTasks, task)
	s.taskMu.Unlock()
}

// addProviderSub adds a provider subscription to be cleaned up on session close.
func (s *Session) addProviderSub(sub Subscription) {
	if sub == nil {
		return
	}
	s.providerSubsMu.Lock()
	s.providerSubs = append(s.providerSubs, sub)
	s.providerSubsMu.Unlock()
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

// processExpirations removes all components that have expired.
// Called by scheduler on each tick.
func (s *Session) processExpirations(nowMs int64) {
	s.expirationMu.Lock()
	if len(s.expirations) == 0 {
		s.expirationMu.Unlock()
		return
	}

	// Collect expired component IDs
	var expired []ComponentID
	for id, expireMs := range s.expirations {
		if nowMs >= expireMs {
			expired = append(expired, id)
		}
	}

	// Remove from expirations map
	for _, id := range expired {
		delete(s.expirations, id)
	}
	s.expirationMu.Unlock()

	// Remove expired components (this calls Detach if implemented)
	for _, id := range expired {
		s.removeComponentByID(id)
	}
}

// removeComponentByID removes a component by its ID.
func (s *Session) removeComponentByID(id ComponentID) {
	s.mu.Lock()

	ptr := s.components[id]
	if ptr == nil {
		s.mu.Unlock()
		return
	}

	// Clear before calling Detach
	s.components[id] = nil
	s.mask.Clear(id)

	s.mu.Unlock()

	// Get type info for Detach and event
	t := s.manager.registry.getType(id)
	if t == nil {
		return
	}

	// Call Detach if implemented
	val := reflect.NewAt(t, ptr).Interface()
	if detachable, ok := val.(Detachable); ok {
		detachable.Detach(s)
	}

	s.Dispatch(&ComponentDetachEvent{
		ComponentType: t,
	})
}
