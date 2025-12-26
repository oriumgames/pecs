package pecs

import (
	"reflect"
	"slices"
	"sync/atomic"
	"unsafe"
)

// Shared[T] references a shared entity's data by its ID.
//
// Unlike Peer[T] which references player data, Shared[T] references entities
// that are shared across multiple players (parties, matches, guilds, etc.).
// The data is cached globally and all references point to the same instance.
//
// Usage:
//
//	type MatchmakingData struct {
//	    CurrentParty Shared[PartyInfo]
//	    ActiveMatch  Shared[MatchInfo]
//	}
//
//	// In a system - PECS injects the resolved data
//	type PartyDisplaySystem struct {
//	    Session *Session
//	    MMData  *MatchmakingData
//	    Party   *PartyInfo `pecs:"shared"` // Resolved from MMData.CurrentParty
//	}
//
//	// In a command - manual resolution
//	func (c PartyInfoCommand) Run(src cmd.Source, out *cmd.Output, tx *world.Tx) {
//	    p, sess := pecs.Command(src)
//	    if sess == nil {
//	        return
//	    }
//	    mmData := pecs.Get[MatchmakingData](sess)
//	    if party, ok := mmData.CurrentParty.Resolve(sess.Manager()); ok {
//	        out.Printf("Party: %s (%d members)", party.Name, len(party.Members))
//	    }
//	}
type Shared[T any] struct {
	id string
}

// Set sets the target entity ID.
func (s *Shared[T]) Set(entityID string) {
	s.id = entityID
}

// Clear removes the target reference.
func (s *Shared[T]) Clear() {
	s.id = ""
}

// ID returns the target entity ID.
func (s *Shared[T]) ID() string {
	return s.id
}

// IsSet returns true if a target is set.
func (s *Shared[T]) IsSet() bool {
	return s.id != ""
}

// Resolve fetches the shared entity's data.
// Returns (nil, false) if the entity is not set, doesn't exist,
// or the data is not available.
func (s *Shared[T]) Resolve(m *Manager) (*T, bool) {
	if m == nil || s.id == "" {
		return nil, false
	}

	t := reflect.TypeFor[T]()
	ptr := m.ResolveShared(s.id, t)
	if ptr == nil {
		return nil, false
	}
	return (*T)(ptr), true
}

// TargetType returns the reflect.Type of the data type T.
func (s *Shared[T]) TargetType() reflect.Type {
	return reflect.TypeFor[T]()
}

// sharedTypeInfo is used for type detection during system analysis.
type sharedTypeInfo interface {
	TargetType() reflect.Type
	isShared()
}

func (s *Shared[T]) isShared() {}

// isSharedType checks if a type is Shared[T].
func isSharedType(t reflect.Type) bool {
	if t.Kind() != reflect.Struct {
		return false
	}
	ptr := reflect.New(t)
	_, ok := ptr.Interface().(sharedTypeInfo)
	return ok
}

// getSharedTargetType extracts the T from Shared[T].
func getSharedTargetType(t reflect.Type) reflect.Type {
	ptr := reflect.New(t)
	if info, ok := ptr.Interface().(sharedTypeInfo); ok {
		return info.TargetType()
	}
	return nil
}

// getSharedID extracts the ID from a Shared[T] at the given pointer.
// Uses unsafe for performance - the caller must ensure ptr points to a valid Shared[T].
func getSharedID(ptr unsafe.Pointer) string {
	// Shared[T] has the same memory layout regardless of T:
	// struct { id string }
	return *(*string)(ptr)
}

// SharedSet[T] references multiple shared entities.
//
// Usage:
//
//	type GuildData struct {
//	    ActiveWars SharedSet[WarInfo]
//	}
//
//	type WarDisplaySystem struct {
//	    Session *Session
//	    Guild   *GuildData
//	    Wars    []*WarInfo `pecs:"shared"` // Resolved from Guild.ActiveWars
//	}
type SharedSet[T any] struct {
	ids atomic.Pointer[[]string]
}

// Set replaces all target entity IDs.
func (ss *SharedSet[T]) Set(entityIDs []string) {
	copied := make([]string, len(entityIDs))
	copy(copied, entityIDs)
	ss.ids.Store(&copied)
}

// Add adds an entity ID to the set.
func (ss *SharedSet[T]) Add(entityID string) {
	for {
		old := ss.ids.Load()
		var newIDs []string
		if old != nil {
			// Check for duplicate
			if slices.Contains(*old, entityID) {
				return
			}
			newIDs = make([]string, len(*old)+1)
			copy(newIDs, *old)
			newIDs[len(*old)] = entityID
		} else {
			newIDs = []string{entityID}
		}
		if ss.ids.CompareAndSwap(old, &newIDs) {
			return
		}
	}
}

// Remove removes an entity ID from the set.
func (ss *SharedSet[T]) Remove(entityID string) {
	for {
		old := ss.ids.Load()
		if old == nil || len(*old) == 0 {
			return
		}

		idx := -1
		for i, id := range *old {
			if id == entityID {
				idx = i
				break
			}
		}
		if idx == -1 {
			return
		}

		newIDs := make([]string, len(*old)-1)
		copy(newIDs, (*old)[:idx])
		copy(newIDs[idx:], (*old)[idx+1:])

		if ss.ids.CompareAndSwap(old, &newIDs) {
			return
		}
	}
}

// Clear removes all target references.
func (ss *SharedSet[T]) Clear() {
	ss.ids.Store(nil)
}

// IDs returns a copy of all target entity IDs.
func (ss *SharedSet[T]) IDs() []string {
	ptr := ss.ids.Load()
	if ptr == nil {
		return nil
	}
	copied := make([]string, len(*ptr))
	copy(copied, *ptr)
	return copied
}

// Len returns the number of targets.
func (ss *SharedSet[T]) Len() int {
	ptr := ss.ids.Load()
	if ptr == nil {
		return 0
	}
	return len(*ptr)
}

// Resolve fetches all shared entities' data.
// Returns only successfully resolved data (nil entries are filtered out).
func (ss *SharedSet[T]) Resolve(m *Manager) []*T {
	if m == nil {
		return nil
	}

	ids := ss.IDs()
	if len(ids) == 0 {
		return nil
	}

	t := reflect.TypeFor[T]()
	ptrs := m.ResolveSharedMany(ids, t)

	// Filter nil results
	results := make([]*T, 0, len(ptrs))
	for _, ptr := range ptrs {
		if ptr != nil {
			results = append(results, (*T)(ptr))
		}
	}
	return results
}

// TargetType returns the reflect.Type of the data type T.
func (ss *SharedSet[T]) TargetType() reflect.Type {
	return reflect.TypeFor[T]()
}

type sharedSetTypeInfo interface {
	TargetType() reflect.Type
	isSharedSet()
}

func (ss *SharedSet[T]) isSharedSet() {}

// isSharedSetType checks if a type is SharedSet[T].
func isSharedSetType(t reflect.Type) bool {
	if t.Kind() != reflect.Struct {
		return false
	}
	ptr := reflect.New(t)
	_, ok := ptr.Interface().(sharedSetTypeInfo)
	return ok
}

// getSharedSetTargetType extracts the T from SharedSet[T].
func getSharedSetTargetType(t reflect.Type) reflect.Type {
	ptr := reflect.New(t)
	if info, ok := ptr.Interface().(sharedSetTypeInfo); ok {
		return info.TargetType()
	}
	return nil
}

// getSharedSetIDs extracts the IDs from a SharedSet[T] at the given pointer.
func getSharedSetIDs(ptr unsafe.Pointer) []string {
	// SharedSet[T] layout: struct { ids atomic.Pointer[[]string] }
	idsPtr := (*atomic.Pointer[[]string])(ptr)
	loaded := idsPtr.Load()
	if loaded == nil {
		return nil
	}
	return *loaded
}
