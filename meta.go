package pecs

import (
	"fmt"
	"reflect"
	"slices"
	"sync"
)

// SystemMeta holds pre-computed metadata about a system type.
// This is computed once at registration time and reused for all executions.
type SystemMeta struct {
	// Type is the reflect.Type of the system struct
	Type reflect.Type

	// Name is the type name for debugging
	Name string

	// RequireMask is the bitmask of required components
	RequireMask Bitmask

	// ExcludeMask is the bitmask of excluded components (Without[T])
	ExcludeMask Bitmask

	// Fields holds injection metadata for each field
	Fields []FieldMeta

	// Windows defines session windows for multi-session systems
	Windows []WindowMeta

	// Stage is the execution stage
	Stage Stage

	// IsMultiSession indicates this is a multi-session system
	IsMultiSession bool

	// Pool is the sync.Pool for this system type
	Pool *sync.Pool

	// Bundle is the bundle this system belongs to
	Bundle *Bundle

	// AccessMeta for conflict detection
	Access AccessMeta
}

// FieldMeta holds metadata about a single injectable field.
type FieldMeta struct {
	// Offset is the field offset in the struct for unsafe injection
	Offset uintptr

	// Name is the field name for debugging
	Name string

	// Kind is the type of field (component, resource, etc.)
	Kind FieldKind

	// ComponentID is the ID of the component type (for component fields)
	ComponentID ComponentID

	// ComponentType is the reflect.Type of the component.
	// For payload fields, this stores the type of the field itself.
	ComponentType reflect.Type

	// Optional indicates the field can be nil
	Optional bool

	// Mutable indicates the field has write access
	Mutable bool

	// WindowIndex indicates which session window this field belongs to
	WindowIndex int

	// RelationSourceField is the name of the field containing the relation
	// (for relation traversal fields)
	RelationSourceField string

	// RelationSourceIndex is the index of the source field in Fields
	RelationSourceIndex int

	// RelationDataOffset is the offset of the Relation/RelationSet field in the source component
	RelationDataOffset uintptr

	// IsSlice indicates this is a slice field (for RelationSet resolution)
	IsSlice bool

	// PeerSourceIndex is the index of the source component field for Peer/PeerSet resolution
	PeerSourceIndex int

	// PeerSourceOffset is the offset of the Peer[T]/PeerSet[T] field in the source component
	PeerSourceOffset uintptr

	// SharedSourceIndex is the index of the source component field for Shared/SharedSet resolution
	SharedSourceIndex int

	// SharedSourceOffset is the offset of the Shared[T]/SharedSet[T] field in the source component
	SharedSourceOffset uintptr
}

// WindowMeta defines a session window in a multi-session system.
type WindowMeta struct {
	// SessionFieldIndex is the index of the *Session field in Fields
	SessionFieldIndex int

	// StartFieldIndex is the first field index for this window
	StartFieldIndex int

	// EndFieldIndex is one past the last field index for this window
	EndFieldIndex int

	// RequireMask is the bitmask of required components for this window
	RequireMask Bitmask

	// ExcludeMask is the bitmask of excluded components for this window
	ExcludeMask Bitmask
}

// AccessMeta describes what components/resources a system reads or writes.
// Used for conflict detection and parallel scheduling.
type AccessMeta struct {
	Reads     []reflect.Type
	Writes    []reflect.Type
	ResReads  []reflect.Type
	ResWrites []reflect.Type

	// Precomputed sets for fast conflict checks
	readsSet     map[reflect.Type]struct{}
	writesSet    map[reflect.Type]struct{}
	resReadsSet  map[reflect.Type]struct{}
	resWritesSet map[reflect.Type]struct{}
}

// PrepareSets precomputes lookup sets from the slice fields for faster conflict checks.
func (a *AccessMeta) PrepareSets() {
	build := func(src []reflect.Type) map[reflect.Type]struct{} {
		if len(src) == 0 {
			return nil
		}
		m := make(map[reflect.Type]struct{}, len(src))
		for _, t := range src {
			m[t] = struct{}{}
		}
		return m
	}
	a.readsSet = build(a.Reads)
	a.writesSet = build(a.Writes)
	a.resReadsSet = build(a.ResReads)
	a.resWritesSet = build(a.ResWrites)
}

// Conflicts returns true if this access pattern conflicts with another.
func (a *AccessMeta) Conflicts(other *AccessMeta) bool {
	// Components
	if other.readsSet != nil {
		for _, w := range a.Writes {
			if _, ok := other.readsSet[w]; ok {
				return true
			}
		}
	} else {
		for _, w := range a.Writes {
			if slices.Contains(other.Reads, w) {
				return true
			}
		}
	}
	if other.writesSet != nil {
		for _, w := range a.Writes {
			if _, ok := other.writesSet[w]; ok {
				return true
			}
		}
		for _, r := range a.Reads {
			if _, ok := other.writesSet[r]; ok {
				return true
			}
		}
	} else {
		for _, w := range a.Writes {
			if slices.Contains(other.Writes, w) {
				return true
			}
		}
		for _, r := range a.Reads {
			if slices.Contains(other.Writes, r) {
				return true
			}
		}
	}

	// Resources
	if other.resReadsSet != nil {
		for _, w := range a.ResWrites {
			if _, ok := other.resReadsSet[w]; ok {
				return true
			}
		}
	} else {
		for _, w := range a.ResWrites {
			if slices.Contains(other.ResReads, w) {
				return true
			}
		}
	}
	if other.resWritesSet != nil {
		for _, w := range a.ResWrites {
			if _, ok := other.resWritesSet[w]; ok {
				return true
			}
		}
		for _, r := range a.ResReads {
			if _, ok := other.resWritesSet[r]; ok {
				return true
			}
		}
	} else {
		for _, w := range a.ResWrites {
			if slices.Contains(other.ResWrites, w) {
				return true
			}
		}
		for _, r := range a.ResReads {
			if slices.Contains(other.ResWrites, r) {
				return true
			}
		}
	}

	return false
}

// analyzeSystem analyzes a system type and returns its metadata.
// The registry parameter is used to register component types for this manager.
func analyzeSystem(systemType reflect.Type, bundle *Bundle, registry *componentRegistry) (*SystemMeta, error) {
	if systemType.Kind() == reflect.Ptr {
		systemType = systemType.Elem()
	}
	if systemType.Kind() != reflect.Struct {
		return nil, fmt.Errorf("system must be a struct, got %v", systemType.Kind())
	}

	meta := &SystemMeta{
		Type:   systemType,
		Name:   systemType.Name(),
		Bundle: bundle,
		Pool: &sync.Pool{
			New: func() any {
				return reflect.New(systemType).Interface()
			},
		},
	}

	var currentWindow *WindowMeta
	var currentWindowIndex int = 0
	var lastComponentFieldIndex int = -1
	var lastComponentField *FieldMeta
	var sessionCount int

	for i := 0; i < systemType.NumField(); i++ {
		field := systemType.Field(i)
		tag := parseTag(field.Tag.Get(tagName))

		fieldMeta := FieldMeta{
			Offset:   field.Offset,
			Name:     field.Name,
			Optional: tag.Optional,
			Mutable:  tag.Mutable,
		}

		// Check for *Session field - starts a new window
		if field.Type == reflect.TypeOf((*Session)(nil)) {
			if sessionCount > 0 {
				currentWindowIndex++
			}
			sessionCount++

			fieldMeta.Kind = KindSession
			fieldMeta.WindowIndex = currentWindowIndex

			if currentWindow != nil {
				currentWindow.EndFieldIndex = len(meta.Fields)
			}
			currentWindow = &WindowMeta{
				SessionFieldIndex: len(meta.Fields),
				StartFieldIndex:   len(meta.Fields),
			}
			meta.Windows = append(meta.Windows, *currentWindow)
			currentWindow = &meta.Windows[len(meta.Windows)-1]

			meta.Fields = append(meta.Fields, fieldMeta)
			continue
		}

		// Check for *Manager field
		if field.Type == reflect.TypeOf((*Manager)(nil)) {
			fieldMeta.Kind = KindManager
			fieldMeta.WindowIndex = currentWindowIndex
			meta.Fields = append(meta.Fields, fieldMeta)
			continue
		}

		// Set window index for all fields
		fieldMeta.WindowIndex = currentWindowIndex

		// Check for phantom types (With[T] and Without[T])
		if isPhantomType(field.Type) {
			compType, isWithout, _ := getPhantomInfo(field.Type)
			if compType != nil {
				compID := registry.register(compType)
				if isWithout {
					fieldMeta.Kind = KindPhantomWithout
					meta.ExcludeMask.Set(compID)
					if currentWindow != nil {
						currentWindow.ExcludeMask.Set(compID)
					}
				} else {
					fieldMeta.Kind = KindPhantomWith
					meta.RequireMask.Set(compID)
					if currentWindow != nil {
						currentWindow.RequireMask.Set(compID)
					}
				}
				fieldMeta.ComponentID = compID
				fieldMeta.ComponentType = compType
			}
			meta.Fields = append(meta.Fields, fieldMeta)
			continue
		}

		// Check for global injection
		if tag.Inject {
			fieldMeta.Kind = KindInjection
			fieldMeta.ComponentType = field.Type
			if field.Type.Kind() == reflect.Ptr {
				fieldMeta.ComponentType = field.Type.Elem()
			}
			meta.Fields = append(meta.Fields, fieldMeta)
			continue
		}

		// Check for resource injection
		if tag.Resource {
			fieldMeta.Kind = KindResource
			fieldMeta.ComponentType = field.Type
			if field.Type.Kind() == reflect.Ptr {
				fieldMeta.ComponentType = field.Type.Elem()
			}
			if tag.Mutable {
				meta.Access.ResWrites = append(meta.Access.ResWrites, fieldMeta.ComponentType)
			} else {
				meta.Access.ResReads = append(meta.Access.ResReads, fieldMeta.ComponentType)
			}
			meta.Fields = append(meta.Fields, fieldMeta)
			continue
		}

		// Check for Peer[T] resolution (pecs:"peer")
		if tag.Peer {
			compType := field.Type
			if compType.Kind() == reflect.Slice {
				fieldMeta.IsSlice = true
				compType = compType.Elem()
			}
			if compType.Kind() == reflect.Ptr {
				compType = compType.Elem()
			}

			fieldMeta.ComponentType = compType

			if fieldMeta.IsSlice {
				fieldMeta.Kind = KindPeerSlice
			} else {
				fieldMeta.Kind = KindPeer
			}

			// Link to previous component field for Peer resolution
			if lastComponentField != nil {
				fieldMeta.PeerSourceIndex = lastComponentFieldIndex

				// Find the Peer[T] or PeerSet[T] field in the source component
				sourceType := lastComponentField.ComponentType
				targetType := compType

				for j := 0; j < sourceType.NumField(); j++ {
					f := sourceType.Field(j)

					// Check for Peer[T]
					if !fieldMeta.IsSlice && isPeerType(f.Type) {
						peerTarget := getPeerTargetType(f.Type)
						if peerTarget == targetType {
							fieldMeta.PeerSourceOffset = f.Offset
							break
						}
					}

					// Check for PeerSet[T]
					if fieldMeta.IsSlice && isPeerSetType(f.Type) {
						peerSetTarget := getPeerSetTargetType(f.Type)
						if peerSetTarget == targetType {
							fieldMeta.PeerSourceOffset = f.Offset
							break
						}
					}
				}
			}

			meta.Fields = append(meta.Fields, fieldMeta)
			continue
		}

		// Check for Shared[T] resolution (pecs:"shared")
		if tag.Shared {
			compType := field.Type
			if compType.Kind() == reflect.Slice {
				fieldMeta.IsSlice = true
				compType = compType.Elem()
			}
			if compType.Kind() == reflect.Ptr {
				compType = compType.Elem()
			}

			fieldMeta.ComponentType = compType

			if fieldMeta.IsSlice {
				fieldMeta.Kind = KindSharedSlice
			} else {
				fieldMeta.Kind = KindShared
			}

			// Link to previous component field for Shared resolution
			if lastComponentField != nil {
				fieldMeta.SharedSourceIndex = lastComponentFieldIndex

				// Find the Shared[T] or SharedSet[T] field in the source component
				sourceType := lastComponentField.ComponentType
				targetType := compType

				for j := 0; j < sourceType.NumField(); j++ {
					f := sourceType.Field(j)

					// Check for Shared[T]
					if !fieldMeta.IsSlice && isSharedType(f.Type) {
						sharedTarget := getSharedTargetType(f.Type)
						if sharedTarget == targetType {
							fieldMeta.SharedSourceOffset = f.Offset
							break
						}
					}

					// Check for SharedSet[T]
					if fieldMeta.IsSlice && isSharedSetType(f.Type) {
						sharedSetTarget := getSharedSetTargetType(f.Type)
						if sharedSetTarget == targetType {
							fieldMeta.SharedSourceOffset = f.Offset
							break
						}
					}
				}
			}

			meta.Fields = append(meta.Fields, fieldMeta)
			continue
		}

		// Check for relation traversal
		if tag.Relation {
			compType := field.Type
			if compType.Kind() == reflect.Slice {
				fieldMeta.IsSlice = true
				compType = compType.Elem()
			}
			if compType.Kind() == reflect.Ptr {
				compType = compType.Elem()
			}

			compID := registry.register(compType)
			fieldMeta.ComponentID = compID
			fieldMeta.ComponentType = compType

			if fieldMeta.IsSlice {
				fieldMeta.Kind = KindRelationSlice
			} else {
				fieldMeta.Kind = KindRelation
			}

			// Link to previous component field for relation resolution
			if lastComponentField != nil {
				fieldMeta.RelationSourceIndex = lastComponentFieldIndex
				fieldMeta.RelationSourceField = lastComponentField.Name

				// Pre-calculate offset of the relation field in the source component
				sourceType := lastComponentField.ComponentType
				targetType := fieldMeta.ComponentType

				for j := 0; j < sourceType.NumField(); j++ {
					f := sourceType.Field(j)
					// We need a pointer to the field type to check interface implementation
					ptr := reflect.New(f.Type)
					if !ptr.CanInterface() {
						continue
					}

					iface, ok := ptr.Interface().(interface{ TargetType() reflect.Type })
					if !ok || iface.TargetType() != targetType {
						continue
					}

					// Check if it matches Relation vs RelationSet expectation
					isSet := isRelationSetType(ptr.Interface())

					if fieldMeta.IsSlice {
						if isSet {
							fieldMeta.RelationDataOffset = f.Offset
							break
						}
					} else {
						isRel := isRelationType(ptr.Interface())
						if !isSet && isRel {
							fieldMeta.RelationDataOffset = f.Offset
							break
						}
					}
				}
			}

			// Add to requires if not optional
			if !tag.Optional {
				meta.RequireMask.Set(compID)
				if currentWindow != nil {
					currentWindow.RequireMask.Set(compID)
				}
			}

			// Track access for conflict detection
			if tag.Mutable {
				meta.Access.Writes = append(meta.Access.Writes, compType)
			} else {
				meta.Access.Reads = append(meta.Access.Reads, compType)
			}

			meta.Fields = append(meta.Fields, fieldMeta)
			continue
		}

		// Check for component field (pointer to struct)
		if field.Type.Kind() == reflect.Ptr && field.Type.Elem().Kind() == reflect.Struct {
			compType := field.Type.Elem()

			// Skip Session pointers (already handled)
			if compType == reflect.TypeOf(Session{}) {
				continue
			}

			compID := registry.register(compType)
			fieldMeta.Kind = KindComponent
			fieldMeta.ComponentID = compID
			fieldMeta.ComponentType = compType

			// Track for relation resolution
			lastComponentFieldIndex = len(meta.Fields)
			lastComponentField = &fieldMeta

			// Update bitmasks
			if !tag.Optional {
				meta.RequireMask.Set(compID)
				if currentWindow != nil {
					currentWindow.RequireMask.Set(compID)
				}
			}

			// Track access for conflict detection
			if tag.Mutable {
				meta.Access.Writes = append(meta.Access.Writes, compType)
			} else {
				meta.Access.Reads = append(meta.Access.Reads, compType)
			}

			meta.Fields = append(meta.Fields, fieldMeta)
			continue
		}

		// Everything else is payload
		fieldMeta.Kind = KindPayload
		fieldMeta.ComponentType = field.Type
		meta.Fields = append(meta.Fields, fieldMeta)
	}

	// Close the last window
	if currentWindow != nil {
		currentWindow.EndFieldIndex = len(meta.Fields)
	}

	// Determine if this is a multi-session system
	meta.IsMultiSession = len(meta.Windows) > 1

	// Pre-compute access sets
	meta.Access.PrepareSets()

	return meta, nil
}
