package pecs

import (
	"reflect"
	"sync/atomic"
	"unsafe"
)

// Peer[T] references another player's component data by their persistent ID.
//
// When resolved:
//   - If the player is on the same server, their local Session component is used directly.
//   - If the player is remote, PECS fetches and syncs their data via the registered PlayerProvider.
//
// Usage:
//
//	type SocialData struct {
//	    BestFriend Peer[FriendProfile]
//	}
//
//	// In a system - PECS injects the resolved data
//	type ShowFriendSystem struct {
//	    Session    *Session
//	    Social     *SocialData
//	    FriendInfo *FriendProfile `pecs:"peer"` // Resolved from Social.BestFriend
//	}
type Peer[T any] struct {
	id string
}

// Set sets the target player by their persistent ID (e.g., XUID).
func (p *Peer[T]) Set(playerID string) {
	p.id = playerID
}

// Clear removes the target reference.
func (p *Peer[T]) Clear() {
	p.id = ""
}

// ID returns the target player's persistent ID.
func (p *Peer[T]) ID() string {
	return p.id
}

// IsSet returns true if a target is set.
func (p *Peer[T]) IsSet() bool {
	return p.id != ""
}

// TargetType returns the reflect.Type of the component T.
func (p *Peer[T]) TargetType() reflect.Type {
	return reflect.TypeOf((*T)(nil)).Elem()
}

// peerTypeInfo is used for type detection during system analysis.
type peerTypeInfo interface {
	TargetType() reflect.Type
	isPeer()
}

func (p *Peer[T]) isPeer() {}

// isPeerType checks if a type is Peer[T].
func isPeerType(t reflect.Type) bool {
	if t.Kind() != reflect.Struct {
		return false
	}
	ptr := reflect.New(t)
	_, ok := ptr.Interface().(peerTypeInfo)
	return ok
}

// getPeerTargetType extracts the T from Peer[T].
func getPeerTargetType(t reflect.Type) reflect.Type {
	ptr := reflect.New(t)
	if info, ok := ptr.Interface().(peerTypeInfo); ok {
		return info.TargetType()
	}
	return nil
}

// getPeerID extracts the ID from a Peer[T] at the given pointer.
// Uses unsafe for performance - the caller must ensure ptr points to a valid Peer[T].
func getPeerID(ptr unsafe.Pointer) string {
	// Peer[T] has the same memory layout regardless of T:
	// struct { id string }
	// A string in Go is: struct { data *byte, len int }
	return *(*string)(ptr)
}

// PeerSet[T] references multiple players' component data.
//
// Usage:
//
//	type PartyData struct {
//	    Members PeerSet[MemberInfo]
//	}
//
//	type PartyDisplaySystem struct {
//	    Session *Session
//	    Party   *PartyData
//	    Members []*MemberInfo `pecs:"peer"` // Resolved from Party.Members
//	}
type PeerSet[T any] struct {
	ids atomic.Pointer[[]string]
}

// Set replaces all target player IDs.
func (ps *PeerSet[T]) Set(playerIDs []string) {
	copied := make([]string, len(playerIDs))
	copy(copied, playerIDs)
	ps.ids.Store(&copied)
}

// Add adds a player ID to the set.
func (ps *PeerSet[T]) Add(playerID string) {
	for {
		old := ps.ids.Load()
		var newIDs []string
		if old != nil {
			// Check for duplicate
			for _, id := range *old {
				if id == playerID {
					return
				}
			}
			newIDs = make([]string, len(*old)+1)
			copy(newIDs, *old)
			newIDs[len(*old)] = playerID
		} else {
			newIDs = []string{playerID}
		}
		if ps.ids.CompareAndSwap(old, &newIDs) {
			return
		}
	}
}

// Remove removes a player ID from the set.
func (ps *PeerSet[T]) Remove(playerID string) {
	for {
		old := ps.ids.Load()
		if old == nil || len(*old) == 0 {
			return
		}

		idx := -1
		for i, id := range *old {
			if id == playerID {
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

		if ps.ids.CompareAndSwap(old, &newIDs) {
			return
		}
	}
}

// Clear removes all target references.
func (ps *PeerSet[T]) Clear() {
	ps.ids.Store(nil)
}

// IDs returns a copy of all target player IDs.
func (ps *PeerSet[T]) IDs() []string {
	ptr := ps.ids.Load()
	if ptr == nil {
		return nil
	}
	copied := make([]string, len(*ptr))
	copy(copied, *ptr)
	return copied
}

// Len returns the number of targets.
func (ps *PeerSet[T]) Len() int {
	ptr := ps.ids.Load()
	if ptr == nil {
		return 0
	}
	return len(*ptr)
}

// TargetType returns the reflect.Type of the component T.
func (ps *PeerSet[T]) TargetType() reflect.Type {
	return reflect.TypeOf((*T)(nil)).Elem()
}

type peerSetTypeInfo interface {
	TargetType() reflect.Type
	isPeerSet()
}

func (ps *PeerSet[T]) isPeerSet() {}

// isPeerSetType checks if a type is PeerSet[T].
func isPeerSetType(t reflect.Type) bool {
	if t.Kind() != reflect.Struct {
		return false
	}
	ptr := reflect.New(t)
	_, ok := ptr.Interface().(peerSetTypeInfo)
	return ok
}

// getPeerSetTargetType extracts the T from PeerSet[T].
func getPeerSetTargetType(t reflect.Type) reflect.Type {
	ptr := reflect.New(t)
	if info, ok := ptr.Interface().(peerSetTypeInfo); ok {
		return info.TargetType()
	}
	return nil
}

// getPeerSetIDs extracts the IDs from a PeerSet[T] at the given pointer.
func getPeerSetIDs(ptr unsafe.Pointer) []string {
	// PeerSet[T] layout: struct { ids atomic.Pointer[[]string] }
	// atomic.Pointer is just an unsafe.Pointer internally
	idsPtr := (*atomic.Pointer[[]string])(ptr)
	loaded := idsPtr.Load()
	if loaded == nil {
		return nil
	}
	return *loaded
}
