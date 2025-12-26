package pecs

import (
	"unsafe"
)

// efaceHeader is the internal representation of an any.
// This matches the runtime's eface struct.
type efaceHeader struct {
	typ  unsafe.Pointer
	data unsafe.Pointer
}

// sliceHeader is the internal representation of a slice.
type sliceHeader struct {
	data unsafe.Pointer
	len  int
	cap  int
}

// ptrValue extracts the pointer from an interface containing a pointer.
// This is faster than reflect.ValueOf(v).Pointer().
func ptrValue(v any) uintptr {
	return uintptr((*efaceHeader)(unsafe.Pointer(&v)).data)
}

// ptrValueRaw extracts the raw pointer from an interface containing a pointer.
// This is faster than unsafe.Pointer(reflect.ValueOf(v).Pointer()).
func ptrValueRaw(v any) unsafe.Pointer {
	return (*efaceHeader)(unsafe.Pointer(&v)).data
}

// injectSystem injects dependencies into a system instance.
// The system must have been allocated and sessions/resources must be valid.
func injectSystem(system any, sessions []*Session, meta *SystemMeta, bundle *Bundle, manager *Manager) bool {
	if len(sessions) == 0 && !meta.IsGlobal {
		// A non-global system must have sessions to run on.
		return false
	}

	systemPtr := ptrValue(system)
	var currentSession *Session
	if len(sessions) > 0 {
		currentSession = sessions[0]
	}
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
			relPtr := unsafe.Add(lastComponentPtr, field.RelationDataOffset)
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
				clearSlice(systemPtr, field.Offset)
				continue
			}

			// Unsafe access to RelationSet[T]
			relSetPtr := unsafe.Add(lastComponentPtr, field.RelationDataOffset)
			targetSessions := getRelationSetTargets(relSetPtr)

			// Collect component pointers and set the slice
			ptrs := collectComponentPtrs(targetSessions, field.ComponentID, nil)
			setSliceFromPtrs(systemPtr, field.Offset, ptrs)

		case KindResource:
			if manager == nil {
				return false
			}
			res := manager.getResource(field.ComponentType)
			if res == nil {
				return false // Resource not found
			}
			setFieldPtr(systemPtr, field.Offset, res)

		case KindPhantomWith, KindPhantomWithout:
			// Phantom types don't need injection - filtering already done
			continue

		case KindPayload:
			// Payload fields must be zeroed to prevent leakage between sessions
			// when reusing the same system instance (e.g. in scheduler loops)
			zeroField(systemPtr, field.Offset, field.Size)

		case KindPeer:
			// Resolve Peer[T] - single remote player's component
			if lastComponentPtr == nil {
				if !field.Optional {
					return false
				}
				setFieldPtr(systemPtr, field.Offset, nil)
				continue
			}

			// Get player ID from Peer[T] in source component
			peerPtr := unsafe.Add(lastComponentPtr, field.PeerSourceOffset)
			playerID := getPeerID(peerPtr)

			if playerID == "" {
				if !field.Optional {
					return false
				}
				setFieldPtr(systemPtr, field.Offset, nil)
				continue
			}

			// Resolve via manager (handles local-first optimization)
			compPtr := manager.ResolvePeer(playerID, field.ComponentType)
			if compPtr == nil && !field.Optional {
				return false
			}
			setFieldPtr(systemPtr, field.Offset, compPtr)

		case KindPeerSlice:
			// Resolve PeerSet[T] - multiple remote players' components
			if lastComponentPtr == nil {
				clearSlice(systemPtr, field.Offset)
				continue
			}

			// Get player IDs from PeerSet[T] in source component
			peerSetPtr := unsafe.Add(lastComponentPtr, field.PeerSourceOffset)
			playerIDs := getPeerSetIDs(peerSetPtr)

			if len(playerIDs) == 0 {
				clearSlice(systemPtr, field.Offset)
				continue
			}

			// Batch resolve via manager
			compPtrs := manager.ResolvePeers(playerIDs, field.ComponentType)

			// Filter nil entries and set the slice
			ptrs := collectNonNilPtrs(compPtrs, nil)
			setSliceFromPtrs(systemPtr, field.Offset, ptrs)

		case KindShared:
			// Resolve Shared[T] - single shared entity's data
			if lastComponentPtr == nil {
				if !field.Optional {
					return false
				}
				setFieldPtr(systemPtr, field.Offset, nil)
				continue
			}

			// Get entity ID from Shared[T] in source component
			sharedPtr := unsafe.Add(lastComponentPtr, field.SharedSourceOffset)
			entityID := getSharedID(sharedPtr)

			if entityID == "" {
				if !field.Optional {
					return false
				}
				setFieldPtr(systemPtr, field.Offset, nil)
				continue
			}

			// Resolve via manager
			dataPtr := manager.ResolveShared(entityID, field.ComponentType)
			if dataPtr == nil && !field.Optional {
				return false
			}
			setFieldPtr(systemPtr, field.Offset, dataPtr)

		case KindSharedSlice:
			// Resolve SharedSet[T] - multiple shared entities' data
			if lastComponentPtr == nil {
				clearSlice(systemPtr, field.Offset)
				continue
			}

			// Get entity IDs from SharedSet[T] in source component
			sharedSetPtr := unsafe.Add(lastComponentPtr, field.SharedSourceOffset)
			entityIDs := getSharedSetIDs(sharedSetPtr)

			if len(entityIDs) == 0 {
				clearSlice(systemPtr, field.Offset)
				continue
			}

			// Batch resolve via manager
			dataPtrs := manager.ResolveSharedMany(entityIDs, field.ComponentType)

			// Filter nil entries and set the slice
			ptrs := collectNonNilPtrs(dataPtrs, nil)
			setSliceFromPtrs(systemPtr, field.Offset, ptrs)
		}
	}

	return true
}

// zeroSystem zeros all pointer fields in a system for pool reuse.
func zeroSystem(system any, meta *SystemMeta) {
	systemPtr := ptrValue(system)

	for i := range meta.Fields {
		field := &meta.Fields[i]

		switch field.Kind {
		case KindSession, KindManager, KindComponent, KindRelation, KindResource, KindPeer, KindShared:
			setFieldPtr(systemPtr, field.Offset, nil)

		case KindRelationSlice, KindPeerSlice, KindSharedSlice:
			// Zero the slice length (keep capacity)
			clearSlice(systemPtr, field.Offset)

		case KindPayload:
			// Zero payload fields
			zeroField(systemPtr, field.Offset, field.Size)
		}
	}
}

// setFieldPtr sets a pointer field at the given offset.
func setFieldPtr(base uintptr, offset uintptr, value unsafe.Pointer) {
	*(*unsafe.Pointer)(unsafe.Pointer(base + offset)) = value
}

// clearSlice sets a slice's length to 0 while preserving capacity.
// This is faster than using reflection.
func clearSlice(base uintptr, offset uintptr) {
	header := (*sliceHeader)(unsafe.Pointer(base + offset))
	header.len = 0
}

// zeroField zeros a field of the given size at the given offset.
func zeroField(base uintptr, offset uintptr, size uintptr) {
	if size == 0 {
		return
	}
	ptr := unsafe.Pointer(base + offset)
	// Use a byte slice view to zero the memory
	bytes := unsafe.Slice((*byte)(ptr), size)
	clear(bytes)
}

// ptrSlice is the underlying representation of any []*T slice.
// All pointer slices have the same memory layout regardless of T.
type ptrSlice struct {
	data unsafe.Pointer
	len  int
	cap  int
}

// setSliceFromPtrs sets a pointer slice field from collected pointers.
// This works because all []*T slices have the same underlying layout.
func setSliceFromPtrs(base uintptr, offset uintptr, ptrs []unsafe.Pointer) {
	dst := (*ptrSlice)(unsafe.Pointer(base + offset))

	if len(ptrs) == 0 {
		dst.len = 0
		return
	}

	// If we have enough capacity, reuse the existing backing array
	if dst.cap >= len(ptrs) {
		// Copy pointers into existing backing array
		existing := unsafe.Slice((*unsafe.Pointer)(dst.data), dst.cap)
		copy(existing, ptrs)
		dst.len = len(ptrs)
		return
	}

	// Need to allocate new backing array - use the slice we built
	dst.data = unsafe.Pointer(&ptrs[0])
	dst.len = len(ptrs)
	dst.cap = len(ptrs)
}

// collectComponentPtrs collects component pointers from sessions into a reusable buffer.
func collectComponentPtrs(sessions []*Session, compID ComponentID, buf []unsafe.Pointer) []unsafe.Pointer {
	// Reuse buffer if possible
	result := buf[:0]

	for _, s := range sessions {
		s.mu.RLock()
		ptr := s.getComponentUnsafe(compID)
		s.mu.RUnlock()

		if ptr != nil {
			result = append(result, ptr)
		}
	}

	return result
}

// collectNonNilPtrs filters nil entries from a pointer slice.
func collectNonNilPtrs(ptrs []unsafe.Pointer, buf []unsafe.Pointer) []unsafe.Pointer {
	result := buf[:0]

	for _, ptr := range ptrs {
		if ptr != nil {
			result = append(result, ptr)
		}
	}

	return result
}
