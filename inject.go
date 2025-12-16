package pecs

import (
	"reflect"
	"sync"
	"unsafe"
)

// injectSystem injects dependencies into a system instance.
// The system must have been allocated and sessions/resources must be valid.
func injectSystem(system any, sessions []*Session, meta *SystemMeta, bundle *Bundle, manager *Manager) bool {
	if len(sessions) == 0 {
		return false
	}

	systemPtr := reflect.ValueOf(system).Pointer()
	currentSession := sessions[0]
	currentWindowIndex := 0

	// Track the last component field for relation resolution
	var lastComponentPtr unsafe.Pointer

	for i := range meta.Fields {
		field := &meta.Fields[i]

		// Update current session for multi-session systems
		if field.WindowIndex != currentWindowIndex {
			if field.WindowIndex < len(sessions) {
				currentSession = sessions[field.WindowIndex]
				currentWindowIndex = field.WindowIndex
				lastComponentPtr = nil
			} else {
				return false // Not enough sessions
			}
		}

		switch field.Kind {
		case KindSession:
			setFieldPtr(systemPtr, field.Offset, unsafe.Pointer(currentSession))

		case KindManager:
			if manager == nil {
				return false
			}
			setFieldPtr(systemPtr, field.Offset, unsafe.Pointer(manager))

		case KindComponent:
			currentSession.mu.RLock()
			ptr := currentSession.getComponentUnsafe(field.ComponentID)
			currentSession.mu.RUnlock()

			if ptr == nil && !field.Optional {
				return false // Required component missing
			}
			setFieldPtr(systemPtr, field.Offset, ptr)

			// Track for relation resolution
			lastComponentPtr = ptr

		case KindRelation:
			// Find the relation in the last component
			if lastComponentPtr == nil {
				if !field.Optional {
					return false
				}
				setFieldPtr(systemPtr, field.Offset, nil)
				continue
			}

			// Unsafe access to Relation[T].target
			// We trust RelationDataOffset from meta analysis to point to the Relation struct
			// and we know 'target *Session' is the first field (offset 0).
			relPtr := unsafe.Pointer(uintptr(lastComponentPtr) + field.RelationDataOffset)
			targetSession := *(*(*Session))(relPtr)

			if targetSession == nil || targetSession.closed.Load() {
				if !field.Optional {
					return false
				}
				setFieldPtr(systemPtr, field.Offset, nil)
				continue
			}

			// Get component from target session
			targetSession.mu.RLock()
			compPtr := targetSession.getComponentUnsafe(field.ComponentID)
			targetSession.mu.RUnlock()

			if compPtr == nil {
				if !field.Optional {
					return false
				}
				setFieldPtr(systemPtr, field.Offset, nil)
				continue
			}

			setFieldPtr(systemPtr, field.Offset, compPtr)

		case KindRelationSlice:
			// Find the relation set in the last component
			if lastComponentPtr == nil {
				// Set empty slice
				setEmptySlice(systemPtr, field.Offset, field.ComponentType)
				continue
			}

			// Unsafe access to RelationSet[T]
			relSetPtr := unsafe.Pointer(uintptr(lastComponentPtr) + field.RelationDataOffset)
			targetSessions := getRelationSetTargets(relSetPtr)

			// Get existing slice for capacity reuse
			slicePtr := unsafe.Pointer(systemPtr + field.Offset)
			existingSlice := reflect.NewAt(reflect.SliceOf(reflect.PointerTo(field.ComponentType)), slicePtr).Elem()

			slice := makeComponentSlice(targetSessions, field.ComponentID, field.ComponentType, existingSlice)
			setSliceField(systemPtr, field.Offset, slice)

		case KindResource:
			if bundle == nil {
				return false
			}
			res := bundle.getResource(field.ComponentType)
			if res == nil {
				return false // Resource not found
			}
			setFieldPtr(systemPtr, field.Offset, res)

		case KindInjection:
			if manager == nil {
				return false
			}
			inj := manager.getInjection(field.ComponentType)
			if inj == nil {
				return false // Injection not found
			}
			setFieldPtr(systemPtr, field.Offset, inj)

		case KindPhantomWith, KindPhantomWithout:
			// Phantom types don't need injection - filtering already done
			continue

		case KindPayload:
			// Payload fields must be zeroed to prevent leakage between sessions
			// when reusing the same system instance (e.g. in scheduler loops)
			zeroPayloadField(systemPtr, field)
		}
	}

	return true
}

// zeroSystem zeros all pointer fields in a system for pool reuse.
func zeroSystem(system any, meta *SystemMeta) {
	systemPtr := reflect.ValueOf(system).Pointer()

	for i := range meta.Fields {
		field := &meta.Fields[i]

		switch field.Kind {
		case KindSession, KindManager, KindComponent, KindRelation, KindResource, KindInjection:
			setFieldPtr(systemPtr, field.Offset, nil)

		case KindRelationSlice:
			// Zero the slice header
			setEmptySlice(systemPtr, field.Offset, field.ComponentType)

		case KindPayload:
			// Zero payload fields based on type
			zeroPayloadField(systemPtr, field)
		}
	}
}

// setFieldPtr sets a pointer field at the given offset.
func setFieldPtr(base uintptr, offset uintptr, value unsafe.Pointer) {
	*(*unsafe.Pointer)(unsafe.Pointer(base + offset)) = value
}

// setEmptySlice sets an empty slice at the given offset while preserving capacity.
func setEmptySlice(base uintptr, offset uintptr, elemType reflect.Type) {
	// Get pointer to the slice
	slicePtr := unsafe.Pointer(base + offset)

	// Use reflect to safely set length to 0 while preserving capacity
	sliceType := reflect.SliceOf(reflect.PointerTo(elemType))
	slice := reflect.NewAt(sliceType, slicePtr).Elem()
	slice.SetLen(0)
}

// setSliceField sets a slice field at the given offset.
func setSliceField(base uintptr, offset uintptr, slice reflect.Value) {
	dst := reflect.NewAt(slice.Type(), unsafe.Pointer(base+offset)).Elem()
	dst.Set(slice)
}

// zeroPayloadField zeros a payload field based on its type.
func zeroPayloadField(base uintptr, field *FieldMeta) {
	if field.ComponentType == nil {
		return
	}

	v := reflect.NewAt(field.ComponentType, unsafe.Pointer(base+field.Offset)).Elem()
	v.Set(reflect.Zero(field.ComponentType))
}

// relationSetLayout matches the memory layout of RelationSet[T].
type relationSetLayout struct {
	mu      sync.RWMutex
	targets map[*Session]struct{}
}

// getRelationSetTargetsUnsafe gets all target sessions from a RelationSet[T] pointer.
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

// makeComponentSlice creates a slice of component pointers from sessions.
func makeComponentSlice(sessions []*Session, compID ComponentID, compType reflect.Type, reuse reflect.Value) reflect.Value {
	slice := reuse
	slice.SetLen(0)

	if slice.Cap() < len(sessions) {
		sliceType := reflect.SliceOf(reflect.PointerTo(compType))
		slice = reflect.MakeSlice(sliceType, 0, len(sessions))
	}

	for _, s := range sessions {
		s.mu.RLock()
		ptr := s.getComponentUnsafe(compID)
		s.mu.RUnlock()

		if ptr != nil {
			compValue := reflect.NewAt(compType, ptr)
			slice = reflect.Append(slice, compValue)
		}
	}

	return slice
}

// clearRelationsReflect clears all relations to a target session in a component.
func clearRelationsReflect(componentPtr any, target *Session) {
	val := reflect.ValueOf(componentPtr)
	if val.Kind() == reflect.Ptr {
		val = val.Elem()
	}
	if val.Kind() != reflect.Struct {
		return
	}

	typ := val.Type()
	for i := 0; i < val.NumField(); i++ {
		field := val.Field(i)
		fieldType := typ.Field(i).Type

		if !field.CanInterface() {
			continue
		}

		// Check for Relation[T]
		if fieldType.Kind() == reflect.Struct {
			typeName := fieldType.String()
			if len(typeName) > 14 && typeName[:14] == "pecs.Relation[" {
				targetField := field.FieldByName("target")
				if targetField.IsValid() && targetField.CanSet() {
					if targetField.Interface() == target {
						targetField.Set(reflect.Zero(targetField.Type()))
					}
				}
			}
			// Check for RelationSet[T]
			if len(typeName) > 17 && typeName[:17] == "pecs.RelationSet[" {
				targetsField := field.FieldByName("targets")
				if targetsField.IsValid() && targetsField.CanInterface() {
					if targets, ok := targetsField.Interface().(map[*Session]struct{}); ok {
						delete(targets, target)
					}
				}
			}
		}
	}
}
