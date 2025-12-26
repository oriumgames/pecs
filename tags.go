package pecs

import (
	"reflect"
	"strings"
)

// Tag constants
const (
	tagName = "pecs"
)

// Tag modifiers
const (
	modMut    = "mut"    // Mutable access
	modOpt    = "opt"    // Optional (nil if missing)
	modRel    = "rel"    // Relation traversal
	modRes    = "res"    // Resource injection
	modPeer   = "peer"   // Peer[T] resolution (remote player data)
	modShared = "shared" // Shared[T] resolution (shared entity data)
)

// FieldKind represents the type of field for injection.
type FieldKind int

const (
	// KindSession indicates a *Session field
	KindSession FieldKind = iota
	// KindManager indicates a *Manager field
	KindManager
	// KindComponent indicates a component field
	KindComponent
	// KindRelation indicates a relation traversal field
	KindRelation
	// KindRelationSlice indicates a relation set traversal field (slice)
	KindRelationSlice
	// KindResource indicates a resource field
	KindResource
	// KindPhantomWith indicates a With[T] phantom type
	KindPhantomWith
	// KindPhantomWithout indicates a Without[T] phantom type
	KindPhantomWithout
	// KindPayload indicates a non-injected payload field
	KindPayload
	// KindPeer indicates a Peer[T] resolution field (single remote player)
	KindPeer
	// KindPeerSlice indicates a PeerSet[T] resolution field (multiple remote players)
	KindPeerSlice
	// KindShared indicates a Shared[T] resolution field (single shared entity)
	KindShared
	// KindSharedSlice indicates a SharedSet[T] resolution field (multiple shared entities)
	KindSharedSlice
	// KindPeerSource indicates a Peer[T] field in a component (source for resolution)
	KindPeerSource
	// KindPeerSetSource indicates a PeerSet[T] field in a component (source for resolution)
	KindPeerSetSource
	// KindSharedSource indicates a Shared[T] field in a component (source for resolution)
	KindSharedSource
	// KindSharedSetSource indicates a SharedSet[T] field in a component (source for resolution)
	KindSharedSetSource
)

// String returns the string representation of FieldKind.
func (k FieldKind) String() string {
	switch k {
	case KindSession:
		return "Session"
	case KindManager:
		return "Manager"
	case KindComponent:
		return "Component"
	case KindRelation:
		return "Relation"
	case KindRelationSlice:
		return "RelationSlice"
	case KindResource:
		return "Resource"
	case KindPhantomWith:
		return "PhantomWith"
	case KindPhantomWithout:
		return "PhantomWithout"
	case KindPayload:
		return "Payload"
	case KindPeer:
		return "Peer"
	case KindPeerSlice:
		return "PeerSlice"
	case KindShared:
		return "Shared"
	case KindSharedSlice:
		return "SharedSlice"
	case KindPeerSource:
		return "PeerSource"
	case KindPeerSetSource:
		return "PeerSetSource"
	case KindSharedSource:
		return "SharedSource"
	case KindSharedSetSource:
		return "SharedSetSource"
	default:
		return "Unknown"
	}
}

// TagInfo holds parsed tag information.
type TagInfo struct {
	Mutable  bool // pecs:"mut"
	Optional bool // pecs:"opt"
	Relation bool // pecs:"rel"
	Resource bool // pecs:"res"
	Peer     bool // pecs:"peer"
	Shared   bool // pecs:"shared"
}

// parseTag parses a pecs struct tag.
func parseTag(tag string) TagInfo {
	info := TagInfo{}
	if tag == "" {
		return info
	}

	parts := strings.SplitSeq(tag, ",")
	for part := range parts {
		part = strings.TrimSpace(part)
		switch part {
		case modMut:
			info.Mutable = true
		case modOpt:
			info.Optional = true
		case modRel:
			info.Relation = true
		case modRes:
			info.Resource = true
		case modPeer:
			info.Peer = true
		case modShared:
			info.Shared = true
		}
	}

	return info
}

// hasTag checks if a tag contains a specific modifier.
func hasTag(tag string, mod string) bool {
	if tag == "" {
		return false
	}
	parts := strings.SplitSeq(tag, ",")
	for part := range parts {
		if strings.TrimSpace(part) == mod {
			return true
		}
	}
	return false
}

// With is a phantom type that indicates a component must exist for the system to run.
// The component is not injected into the field - it's only used for filtering.
//
// Usage:
//
//	type MySystem struct {
//	    Session *pecs.Session
//	    _ pecs.With[VampireTag] // Only run if VampireTag exists
//	}
type With[T any] struct{}

// Without is a phantom type that indicates a component must NOT exist for the system to run.
// The system will be skipped if the component is present on the session.
//
// Usage:
//
//	type MySystem struct {
//	    Session *pecs.Session
//	    _ pecs.Without[Spectator] // Skip if Spectator exists
//	}
type Without[T any] struct{}

// PhantomTypeInfo provides component type information for phantom types.
type PhantomTypeInfo interface {
	ComponentType() reflect.Type
	IsWithout() bool
}

// ComponentType implements PhantomTypeInfo for With[T].
func (With[T]) ComponentType() reflect.Type {
	return reflect.TypeFor[T]()
}

// IsWithout implements PhantomTypeInfo for With[T].
func (With[T]) IsWithout() bool {
	return false
}

// ComponentType implements PhantomTypeInfo for Without[T].
func (Without[T]) ComponentType() reflect.Type {
	return reflect.TypeFor[T]()
}

// IsWithout implements PhantomTypeInfo for Without[T].
func (Without[T]) IsWithout() bool {
	return true
}

// phantomTypeInfoType is the reflect.Type of PhantomTypeInfo interface.
var phantomTypeInfoType = reflect.TypeFor[PhantomTypeInfo]()

// isPhantomType checks if a type implements PhantomTypeInfo.
func isPhantomType(t reflect.Type) bool {
	return t.Implements(phantomTypeInfoType)
}

// getPhantomInfo extracts component type and kind from a phantom type.
func getPhantomInfo(t reflect.Type) (compType reflect.Type, isWithout bool, ok bool) {
	if !t.Implements(phantomTypeInfoType) {
		return nil, false, false
	}

	// Create an instance to call the interface methods
	v := reflect.New(t).Elem().Interface().(PhantomTypeInfo)
	return v.ComponentType(), v.IsWithout(), true
}
