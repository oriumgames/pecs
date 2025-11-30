package pecs

import (
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"unsafe"
)

// ComponentID is a unique identifier for a component type.
// Valid IDs range from 0 to 255.
type ComponentID uint8

// MaxComponents is the maximum number of component types supported.
const MaxComponents = 255

// componentRegistry manages component type registration with lock-free reads.
// Component IDs are assigned sequentially and cached for fast lookup.
// sync.Map provides lock-free reads for the hot path (checking registered types)
// while still allowing safe concurrent registration.
type componentRegistry struct {
	// types maps reflect.Type to ComponentID using sync.Map for lock-free reads
	// This is the hot path - components are registered once but looked up constantly
	types sync.Map // map[reflect.Type]ComponentID

	// names and typesArr store component metadata indexed by ComponentID
	// These are written once during registration and read-only afterward
	names    [MaxComponents]string
	typesArr [MaxComponents]reflect.Type

	// nextID is the next available component ID (atomic for lock-free allocation)
	nextID atomic.Uint32

	// arrMu protects writes to names and typesArr arrays
	// Only needed during registration, not for normal lookups
	arrMu sync.RWMutex
}

// globalRegistry is the singleton component registry.
var globalRegistry = &componentRegistry{}

// registerComponentType registers a component type and returns its ID.
// This is called automatically when components are first used.
func registerComponentType(t reflect.Type) ComponentID {
	// Fast path: lock-free read from sync.Map
	// Most calls hit this path since types are registered early
	if id, ok := globalRegistry.types.Load(t); ok {
		return id.(ComponentID)
	}

	// Slow path: need to register new type
	// Atomically allocate ID before attempting to register
	// This ensures each registration attempt gets a unique ID
	newID := ComponentID(globalRegistry.nextID.Add(1) - 1)
	if newID >= MaxComponents {
		panic(fmt.Sprintf("pecs: component limit exceeded (max %d types)", MaxComponents))
	}

	// Try to store our ID atomically
	// LoadOrStore ensures only one goroutine wins if multiple try simultaneously
	actual, loaded := globalRegistry.types.LoadOrStore(t, newID)
	if loaded {
		// Another goroutine registered this type first
		// Use their ID instead of ours (our allocated ID is wasted, but that's rare)
		return actual.(ComponentID)
	}

	// We won the race - store the metadata for this ID
	// This is the only place these arrays are written for this ID
	globalRegistry.arrMu.Lock()
	globalRegistry.names[newID] = t.Name()
	globalRegistry.typesArr[newID] = t
	globalRegistry.arrMu.Unlock()

	return newID
}

// getComponentID returns the ID for a registered component type.
// Returns false if the type is not registered.
func getComponentID(t reflect.Type) (ComponentID, bool) {
	if id, ok := globalRegistry.types.Load(t); ok {
		return id.(ComponentID), true
	}
	return 0, false
}

// componentID returns the ComponentID for type T, registering it if needed.
func componentID[T any]() ComponentID {
	// Use a cached value if available via generic instantiation
	return registerComponentType(reflect.TypeOf((*T)(nil)).Elem())
}

// componentIDFromType returns the ComponentID for the given type.
func componentIDFromType(t reflect.Type) ComponentID {
	return registerComponentType(t)
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
// This function is thread-safe. Since commands and forms are executed synchronously
// with the player, it is safe to add components directly in those contexts.
func Add[T any](s *Session, component *T) {
	if s == nil || component == nil {
		return
	}

	id := componentID[T]()

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
		ComponentType: reflect.TypeOf((*T)(nil)).Elem(),
	})
}

// Remove detaches a component from the session.
// If the component implements Detachable, its Detach method is called first.
//
// Concurrency:
// This function is thread-safe. Since commands and forms are executed synchronously
// with the player, it is safe to remove components directly in those contexts.
func Remove[T any](s *Session) {
	if s == nil {
		return
	}

	id := componentID[T]()

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
		ComponentType: reflect.TypeOf((*T)(nil)).Elem(),
	})
}

// Get retrieves a component from the session.
// Returns nil if the component is not present.
//
// Concurrency:
// This function is thread-safe. Since commands and forms are executed synchronously
// with the player, it is safe to access and modify component fields directly.
// If accessing from a separate goroutine, use Session.Exec for synchronization.
func Get[T any](s *Session) *T {
	if s == nil {
		return nil
	}

	id := componentID[T]()

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
	if s == nil {
		return false
	}

	id := componentID[T]()

	s.mu.RLock()
	has := s.mask.Has(id)
	s.mu.RUnlock()

	return has
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

// ComponentName returns the name of the component type with the given ID.
// Uses read lock since names array is only written during registration.
func ComponentName(id ComponentID) string {
	globalRegistry.arrMu.RLock()
	defer globalRegistry.arrMu.RUnlock()
	return globalRegistry.names[id]
}

// ComponentType returns the reflect.Type of the component with the given ID.
// Uses read lock since typesArr is only written during registration.
func ComponentType(id ComponentID) reflect.Type {
	globalRegistry.arrMu.RLock()
	defer globalRegistry.arrMu.RUnlock()
	return globalRegistry.typesArr[id]
}

// RegisteredComponentCount returns the number of registered component types.
func RegisteredComponentCount() int {
	return int(globalRegistry.nextID.Load())
}
