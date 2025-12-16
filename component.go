package pecs

import (
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"unsafe"
)

// ComponentID is a unique identifier for a component type.
// Valid IDs range from 0 to 255 per manager.
type ComponentID uint8

// MaxComponents is the maximum number of component types supported per manager.
const MaxComponents = 255

// componentRegistry manages component type registration for a single manager.
// Each manager has its own registry, allowing multiple isolated PECS instances.
type componentRegistry struct {
	// types maps reflect.Type to ComponentID using sync.Map for lock-free reads
	types sync.Map // map[reflect.Type]ComponentID

	// names and typesArr store component metadata indexed by ComponentID
	names    [MaxComponents]string
	typesArr [MaxComponents]reflect.Type

	// nextID is the next available component ID (atomic for lock-free allocation)
	nextID atomic.Uint32

	// arrMu protects writes to names and typesArr arrays
	arrMu sync.RWMutex
}

// newComponentRegistry creates a new component registry.
func newComponentRegistry() *componentRegistry {
	return &componentRegistry{}
}

// register registers a component type and returns its ID.
func (r *componentRegistry) register(t reflect.Type) ComponentID {
	// Fast path: already registered
	if id, ok := r.types.Load(t); ok {
		return id.(ComponentID)
	}

	// Slow path: need to register
	newID := ComponentID(r.nextID.Add(1) - 1)
	if newID >= MaxComponents {
		panic(fmt.Sprintf("pecs: component limit exceeded (max %d types per manager)", MaxComponents))
	}

	actual, loaded := r.types.LoadOrStore(t, newID)
	if loaded {
		// Another goroutine registered this type first
		// Use their ID instead of ours (our allocated ID is wasted, but that's rare)
		return actual.(ComponentID)
	}

	r.arrMu.Lock()
	r.names[newID] = t.Name()
	r.typesArr[newID] = t
	r.arrMu.Unlock()

	return newID
}

// getID returns the ID for a registered component type.
func (r *componentRegistry) getID(t reflect.Type) (ComponentID, bool) {
	if id, ok := r.types.Load(t); ok {
		return id.(ComponentID), true
	}
	return 0, false
}

// getName returns the name of the component with the given ID.
func (r *componentRegistry) getName(id ComponentID) string {
	r.arrMu.RLock()
	defer r.arrMu.RUnlock()
	return r.names[id]
}

// getType returns the reflect.Type of the component with the given ID.
func (r *componentRegistry) getType(id ComponentID) reflect.Type {
	r.arrMu.RLock()
	defer r.arrMu.RUnlock()
	return r.typesArr[id]
}

// count returns the number of registered component types.
func (r *componentRegistry) count() int {
	return int(r.nextID.Load())
}

// Attachable is implemented by components that need initialization logic
// when attached to a session.
type Attachable interface {
	Attach(s *Session)
}

// Detachable is implemented by components that need cleanup logic
// when detached from a session or when the session closes.
type Detachable interface {
	Detach(s *Session)
}

// Add attaches a component to the session.
// If a component of this type already exists, it is replaced.
// If the component implements Attachable, its Attach method is called.
//
// Concurrency:
// This function is thread-safe.
func Add[T any](s *Session, component *T) {
	if s == nil || component == nil || s.manager == nil {
		return
	}

	t := reflect.TypeOf((*T)(nil)).Elem()
	id := s.manager.registry.register(t)

	s.mu.Lock()

	// Check for existing component and call Detach if needed
	oldPtr := s.components[id]
	if oldPtr != nil {
		if old, ok := any((*T)(oldPtr)).(Detachable); ok {
			s.mu.Unlock()
			old.Detach(s)
			s.mu.Lock()
		}
	}

	// Store new component
	s.components[id] = unsafe.Pointer(component)
	s.mask.Set(id)

	s.mu.Unlock()

	// Call Attach if implemented
	if attachable, ok := any(component).(Attachable); ok {
		attachable.Attach(s)
	}

	s.Dispatch(ComponentAttachEvent{
		ComponentType: t,
	})
}

// Remove detaches a component from the session.
// If the component implements Detachable, its Detach method is called first.
//
// Concurrency:
// This function is thread-safe.
func Remove[T any](s *Session) {
	if s == nil || s.manager == nil {
		return
	}

	t := reflect.TypeOf((*T)(nil)).Elem()
	id, ok := s.manager.registry.getID(t)
	if !ok {
		return // Component type not registered, nothing to remove
	}

	s.mu.Lock()

	ptr := s.components[id]
	if ptr == nil {
		s.mu.Unlock()
		return
	}

	// Clear before calling Detach to prevent re-entrancy issues
	s.components[id] = nil
	s.mask.Clear(id)

	s.mu.Unlock()

	// Call Detach if implemented
	if component, ok := any((*T)(ptr)).(Detachable); ok {
		component.Detach(s)
	}

	s.Dispatch(ComponentDetachEvent{
		ComponentType: t,
	})
}

// Get retrieves a component from the session.
// Returns nil if the component is not present.
//
// Concurrency:
// This function is thread-safe.
func Get[T any](s *Session) *T {
	if s == nil || s.manager == nil {
		return nil
	}

	t := reflect.TypeOf((*T)(nil)).Elem()
	id, ok := s.manager.registry.getID(t)
	if !ok {
		return nil // Component type not registered
	}

	s.mu.RLock()
	ptr := s.components[id]
	s.mu.RUnlock()

	if ptr == nil {
		return nil
	}
	return (*T)(ptr)
}

// Has checks if a component type is present on the session.
//
// Concurrency:
// This function is fully thread-safe and can be called from any goroutine.
func Has[T any](s *Session) bool {
	if s == nil || s.manager == nil {
		return false
	}

	t := reflect.TypeOf((*T)(nil)).Elem()
	id, ok := s.manager.registry.getID(t)
	if !ok {
		return false // Component type not registered
	}

	s.mu.RLock()
	has := s.mask.Has(id)
	s.mu.RUnlock()

	return has
}

// GetOrAdd retrieves a component from the session, or adds the default if missing.
// Returns the existing component if present, otherwise adds defaultVal and returns it.
//
// Concurrency:
// This function is thread-safe.
func GetOrAdd[T any](s *Session, defaultVal *T) *T {
	if s == nil || s.manager == nil {
		return nil
	}

	t := reflect.TypeOf((*T)(nil)).Elem()
	id := s.manager.registry.register(t)

	// Fast path: check if already exists
	s.mu.RLock()
	ptr := s.components[id]
	s.mu.RUnlock()

	if ptr != nil {
		return (*T)(ptr)
	}

	// Slow path: need to add
	s.mu.Lock()
	// Double-check after acquiring write lock
	ptr = s.components[id]
	if ptr != nil {
		s.mu.Unlock()
		return (*T)(ptr)
	}

	// Add the default value
	s.components[id] = unsafe.Pointer(defaultVal)
	s.mask.Set(id)
	s.mu.Unlock()

	// Call Attach if implemented
	if attachable, ok := any(defaultVal).(Attachable); ok {
		attachable.Attach(s)
	}

	s.Dispatch(ComponentAttachEvent{
		ComponentType: t,
	})

	return defaultVal
}

// getComponentUnsafe retrieves a component by ID without locking.
// Only safe to call when session lock is already held or within PECS execution.
func (s *Session) getComponentUnsafe(id ComponentID) unsafe.Pointer {
	return s.components[id]
}

// hasComponentUnsafe checks component presence by ID without locking.
func (s *Session) hasComponentUnsafe(id ComponentID) bool {
	return s.mask.Has(id)
}

// addComponentUnsafe adds a component by ID without locking.
// Does not call lifecycle hooks.
func (s *Session) addComponentUnsafe(id ComponentID, ptr unsafe.Pointer) {
	s.components[id] = ptr
	s.mask.Set(id)
}

// removeComponentUnsafe removes a component by ID without locking.
// Does not call lifecycle hooks.
func (s *Session) removeComponentUnsafe(id ComponentID) unsafe.Pointer {
	ptr := s.components[id]
	s.components[id] = nil
	s.mask.Clear(id)
	return ptr
}
