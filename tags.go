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
	modMut = "mut" // Mutable access
	modOpt = "opt" // Optional (nil if missing)
	modRel = "rel" // Relation traversal
	modRes = "res" // Resource injection
	modInj = "inj" // Global injection
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
