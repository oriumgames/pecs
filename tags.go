package pecs

import (
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
	modInj    = "inj"    // Global injection
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
	// KindResource indicates a bundle resource field
	KindResource
	// KindInjection indicates a global injection field
	KindInjection
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
	case KindInjection:
		return "Injection"
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
	Inject   bool // pecs:"inj"
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
		case modInj:
			info.Inject = true
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
