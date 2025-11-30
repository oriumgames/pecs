package pecs

import (
	"reflect"
	"sync"
)

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

// Target returns the raw target session pointer.
// This is primarily for internal use.
func (r *Relation[T]) Target() *Session {
	return r.target
}

// TargetType returns the reflect.Type of the component the target must have.
func (r *Relation[T]) TargetType() reflect.Type {
	return reflect.TypeOf((*T)(nil)).Elem()
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

// Iter returns a channel that yields all valid target sessions.
// Sessions that are closed are automatically removed during iteration.
func (rs *RelationSet[T]) Iter() <-chan *Session {
	ch := make(chan *Session)
	go func() {
		defer close(ch)
		rs.mu.RLock()
		targets := make([]*Session, 0, len(rs.targets))
		for target := range rs.targets {
			targets = append(targets, target)
		}
		rs.mu.RUnlock()

		var toRemove []*Session
		for _, target := range targets {
			if target.closed.Load() {
				toRemove = append(toRemove, target)
				continue
			}
			ch <- target
		}

		// Clean up closed sessions
		if len(toRemove) > 0 {
			rs.mu.Lock()
			for _, target := range toRemove {
				delete(rs.targets, target)
			}
			rs.mu.Unlock()
		}
	}()
	return ch
}

// All returns a slice of all valid target sessions.
// This is more efficient than Iter() when you need all sessions at once.
func (rs *RelationSet[T]) All() []*Session {
	rs.mu.RLock()
	targets := make([]*Session, 0, len(rs.targets))
	for target := range rs.targets {
		if !target.closed.Load() {
			targets = append(targets, target)
		}
	}
	rs.mu.RUnlock()
	return targets
}

// AllValid returns all sessions that are valid and have the required component.
func (rs *RelationSet[T]) AllValid() []*Session {
	rs.mu.RLock()
	targets := make([]*Session, 0, len(rs.targets))
	for target := range rs.targets {
		if !target.closed.Load() && Has[T](target) {
			targets = append(targets, target)
		}
	}
	rs.mu.RUnlock()
	return targets
}

// Targets returns a copy of the underlying target map.
// This is primarily for internal use.
func (rs *RelationSet[T]) Targets() map[*Session]struct{} {
	rs.mu.RLock()
	defer rs.mu.RUnlock()
	if rs.targets == nil {
		return nil
	}
	result := make(map[*Session]struct{}, len(rs.targets))
	for k, v := range rs.targets {
		result[k] = v
	}
	return result
}

// TargetType returns the reflect.Type of the component targets must have.
func (rs *RelationSet[T]) TargetType() reflect.Type {
	return reflect.TypeOf((*T)(nil)).Elem()
}

// relationCleaner is an interface for types that contain relations.
type relationCleaner interface {
	clearRelationsTo(target *Session)
}

// clearRelationsTo removes all relation references to the given target session.
// This is called when a session is closed.
func clearRelationsTo(component any, target *Session) {
	if cleaner, ok := component.(relationCleaner); ok {
		cleaner.clearRelationsTo(target)
		return
	}

	// Use reflection for generic Relation[T] and RelationSet[T] fields
	clearRelationsReflect(component, target)
}

// isRelationType checks if a type is Relation[T].
func isRelationType(t any) bool {
	// Check if it implements the relation interface
	_, ok := t.(interface{ Target() *Session })
	return ok
}

// isRelationSetType checks if a type is RelationSet[T].
func isRelationSetType(t any) bool {
	_, ok := t.(interface{ Targets() map[*Session]struct{} })
	return ok
}

// Resolve retrieves the target session and its component of type T from a Relation.
// Returns (nil, nil, false) if the relation is unset, the target is closed, or the component is missing.
func Resolve[T any](r Relation[T]) (*Session, *T, bool) {
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
