package pecs

import (
	"maps"
	"reflect"
	"sync"
	"unsafe"
)

// Resolved holds a resolved relation with both session and component.
type Resolved[T any] struct {
	Session   *Session
	Component *T
}

// Relation represents a reference from one session to another.
// The type parameter T indicates what component the target session must have.
// This provides type-safe references between players.
//
// Usage:
//
//	type Following struct {
//	    Target pecs.Relation[Health] // Target must have Health component
//	}
type Relation[T any] struct {
	target *Session
}

// Set sets the target session for this relation.
// The target should have a component of type T, though this is validated
// at system execution time, not at set time.
func (r *Relation[T]) Set(target *Session) {
	r.target = target
}

// Clear removes the target reference.
func (r *Relation[T]) Clear() {
	r.target = nil
}

// Get returns the target session, or nil if not set or target is closed.
func (r *Relation[T]) Get() *Session {
	if r.target == nil {
		return nil
	}
	// Check if session is still valid
	if r.target.closed.Load() {
		r.target = nil
		return nil
	}
	return r.target
}

// Valid returns true if the target exists and has the required component.
func (r *Relation[T]) Valid() bool {
	target := r.Get()
	if target == nil {
		return false
	}
	return Has[T](target)
}

// Resolve retrieves the target session and its component of type T.
// Returns (nil, nil, false) if the relation is unset, the target is closed,
// or the component is missing.
func (r *Relation[T]) Resolve() (*Session, *T, bool) {
	s := r.Get()
	if s == nil {
		return nil, nil, false
	}
	comp := Get[T](s)
	if comp == nil {
		return s, nil, false
	}
	return s, comp, true
}

// TargetType returns the reflect.Type of the component the target must have.
func (r *Relation[T]) TargetType() reflect.Type {
	return reflect.TypeFor[T]()
}

// RelationSet represents a set of references to other sessions.
// The type parameter T indicates what component target sessions should have.
//
// Usage:
//
//	type PartyLeader struct {
//	    Members pecs.RelationSet[PartyMember]
//	}
type RelationSet[T any] struct {
	mu      sync.RWMutex
	targets map[*Session]struct{}
}

// Add adds a session to the relation set.
func (rs *RelationSet[T]) Add(target *Session) {
	if target == nil {
		return
	}
	rs.mu.Lock()
	if rs.targets == nil {
		rs.targets = make(map[*Session]struct{})
	}
	rs.targets[target] = struct{}{}
	rs.mu.Unlock()
}

// Remove removes a session from the relation set.
func (rs *RelationSet[T]) Remove(target *Session) {
	if target == nil {
		return
	}
	rs.mu.Lock()
	delete(rs.targets, target)
	rs.mu.Unlock()
}

// Has checks if a session is in the relation set.
func (rs *RelationSet[T]) Has(target *Session) bool {
	if target == nil {
		return false
	}
	rs.mu.RLock()
	_, ok := rs.targets[target]
	rs.mu.RUnlock()
	return ok
}

// Clear removes all sessions from the relation set.
func (rs *RelationSet[T]) Clear() {
	rs.mu.Lock()
	rs.targets = nil
	rs.mu.Unlock()
}

// Len returns the number of sessions in the relation set.
func (rs *RelationSet[T]) Len() int {
	rs.mu.RLock()
	n := len(rs.targets)
	rs.mu.RUnlock()
	return n
}

// All returns all non-closed target sessions.
// Closed sessions are lazily removed on subsequent calls.
func (rs *RelationSet[T]) All() []*Session {
	rs.mu.RLock()
	targets := make([]*Session, 0, len(rs.targets))
	hasClosed := false
	for target := range rs.targets {
		if target.closed.Load() {
			hasClosed = true
		} else {
			targets = append(targets, target)
		}
	}
	rs.mu.RUnlock()

	// Cleanup closed sessions outside of hot path
	if hasClosed {
		rs.cleanupClosed()
	}
	return targets
}

// Resolve returns all valid sessions with their components.
// A session is valid if it's not closed and has the required component T.
// Closed sessions are lazily removed on subsequent calls.
func (rs *RelationSet[T]) Resolve() []Resolved[T] {
	rs.mu.RLock()
	results := make([]Resolved[T], 0, len(rs.targets))
	hasClosed := false
	for target := range rs.targets {
		if target.closed.Load() {
			hasClosed = true
			continue
		}
		comp := Get[T](target)
		if comp != nil {
			results = append(results, Resolved[T]{
				Session:   target,
				Component: comp,
			})
		}
	}
	rs.mu.RUnlock()

	// Cleanup closed sessions outside of hot path
	if hasClosed {
		rs.cleanupClosed()
	}
	return results
}

// TargetType returns the reflect.Type of the component targets must have.
func (rs *RelationSet[T]) TargetType() reflect.Type {
	return reflect.TypeFor[T]()
}

// cleanupClosed removes closed sessions from the set.
func (rs *RelationSet[T]) cleanupClosed() {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	for target := range rs.targets {
		if target.closed.Load() {
			delete(rs.targets, target)
		}
	}
}

// getTargets returns a copy of the underlying target map.
// This is for internal use only.
func (rs *RelationSet[T]) getTargets() map[*Session]struct{} {
	rs.mu.RLock()
	defer rs.mu.RUnlock()
	if rs.targets == nil {
		return nil
	}
	result := make(map[*Session]struct{}, len(rs.targets))
	maps.Copy(result, rs.targets)
	return result
}

// isRelationType checks if a type is Relation[T].
func isRelationType(t any) bool {
	_, ok := t.(interface{ Get() *Session })
	return ok
}

// isRelationSetType checks if a type is RelationSet[T].
func isRelationSetType(t any) bool {
	_, ok := t.(interface{ getTargets() map[*Session]struct{} })
	return ok
}

// relationSetLayout matches the memory layout of RelationSet[T].
type relationSetLayout struct {
	mu      sync.RWMutex
	targets map[*Session]struct{}
}

// getRelationSetTargets gets all target sessions from a RelationSet[T] pointer.
func getRelationSetTargets(ptr unsafe.Pointer) []*Session {
	layout := (*relationSetLayout)(ptr)
	layout.mu.RLock()
	defer layout.mu.RUnlock()

	if layout.targets == nil {
		return nil
	}

	result := make([]*Session, 0, len(layout.targets))
	for s := range layout.targets {
		if !s.closed.Load() {
			result = append(result, s)
		}
	}
	return result
}

// relationLayout matches the memory layout of Relation[T].
// Relation[T] is just: struct { target *Session }
type relationLayout struct {
	target *Session
}

// clearRelationsTo clears all relations to a target session in a component.
// This is called when a session is closed.
// Uses unsafe pointer arithmetic for performance instead of reflection.
func clearRelationsTo(componentPtr any, target *Session) {
	val := reflect.ValueOf(componentPtr)
	if val.Kind() == reflect.Ptr {
		val = val.Elem()
	}
	if val.Kind() != reflect.Struct {
		return
	}

	typ := val.Type()
	basePtr := val.UnsafeAddr()

	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		fieldType := field.Type

		if fieldType.Kind() != reflect.Struct {
			continue
		}

		// Use type name check - this is still needed but we avoid repeated reflection
		typeName := fieldType.String()

		// Check for Relation[T]
		if len(typeName) > 14 && typeName[:14] == "pecs.Relation[" {
			// Direct unsafe access to Relation[T].target
			relPtr := (*relationLayout)(unsafe.Pointer(basePtr + field.Offset))
			if relPtr.target == target {
				relPtr.target = nil
			}
			continue
		}

		// Check for RelationSet[T]
		if len(typeName) > 17 && typeName[:17] == "pecs.RelationSet[" {
			// Direct unsafe access to RelationSet[T]
			relSetPtr := (*relationSetLayout)(unsafe.Pointer(basePtr + field.Offset))
			relSetPtr.mu.Lock()
			delete(relSetPtr.targets, target)
			relSetPtr.mu.Unlock()
		}
	}
}
