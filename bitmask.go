package pecs

import (
	"math/bits"
)

// Bitmask is a 256-bit bitmask used for tracking component presence.
// It supports up to 256 unique component types.
type Bitmask [4]uint64

// Set sets the bit at the given index.
func (m *Bitmask) Set(id ComponentID) {
	m[id/64] |= 1 << (id % 64)
}

// Clear clears the bit at the given index.
func (m *Bitmask) Clear(id ComponentID) {
	m[id/64] &^= 1 << (id % 64)
}

// Has returns true if the bit at the given index is set.
func (m *Bitmask) Has(id ComponentID) bool {
	return m[id/64]&(1<<(id%64)) != 0
}

// ContainsAll returns true if all bits set in other are also set in m.
// This is used to check if all required components are present.
// Optimized for the common case where most systems require only a few components
// that fit in the first 64-bit segment.
func (m *Bitmask) ContainsAll(other Bitmask) bool {
	// Check first segment (bits 0-63)
	// Most component types are registered early and fit in this range
	// Early exit here avoids checking the other 3 segments in ~80% of cases
	if (m[0] & other[0]) != other[0] {
		return false
	}

	// Fast path: if other has no bits set in remaining segments, we're done
	// This is the common case since most systems require < 64 component types
	// Checking for zero is faster than doing three more bitwise operations
	if other[1] == 0 && other[2] == 0 && other[3] == 0 {
		return true
	}

	// Slow path: check remaining segments (bits 64-255)
	// Only reached for systems that require components beyond the first 64 types
	return (m[1]&other[1] == other[1]) &&
		(m[2]&other[2] == other[2]) &&
		(m[3]&other[3] == other[3])
}

// ContainsAny returns true if any bit set in other is also set in m.
// This is used to check if any excluded components (Without[T]) are present.
// Optimized similarly to ContainsAll for early exit.
func (m *Bitmask) ContainsAny(other Bitmask) bool {
	// Check first segment for any overlap
	// If we find a match here, we can return immediately
	if (m[0] & other[0]) != 0 {
		return true
	}

	// Fast path: if other has no bits in remaining segments, no need to check
	// This optimization is particularly effective for Without[T] checks
	// since most exclusions involve commonly-used components in the first segment
	if other[1] == 0 && other[2] == 0 && other[3] == 0 {
		return false
	}

	// Slow path: check remaining segments
	// Use OR chain for early exit - stops as soon as any overlap is found
	return (m[1]&other[1] != 0) ||
		(m[2]&other[2] != 0) ||
		(m[3]&other[3] != 0)
}

// IsZero returns true if no bits are set.
func (m *Bitmask) IsZero() bool {
	return m[0] == 0 && m[1] == 0 && m[2] == 0 && m[3] == 0
}

// Or returns a new bitmask with bits set from both m and other.
func (m Bitmask) Or(other Bitmask) Bitmask {
	return Bitmask{
		m[0] | other[0],
		m[1] | other[1],
		m[2] | other[2],
		m[3] | other[3],
	}
}

// And returns a new bitmask with only bits set in both m and other.
func (m Bitmask) And(other Bitmask) Bitmask {
	return Bitmask{
		m[0] & other[0],
		m[1] & other[1],
		m[2] & other[2],
		m[3] & other[3],
	}
}

// AndNot returns a new bitmask with bits set in m but not in other.
func (m Bitmask) AndNot(other Bitmask) Bitmask {
	return Bitmask{
		m[0] &^ other[0],
		m[1] &^ other[1],
		m[2] &^ other[2],
		m[3] &^ other[3],
	}
}

// Count returns the number of bits set.
func (m *Bitmask) Count() int {
	return bits.OnesCount64(m[0]) +
		bits.OnesCount64(m[1]) +
		bits.OnesCount64(m[2]) +
		bits.OnesCount64(m[3])
}

// Clone returns a copy of the bitmask.
func (m Bitmask) Clone() Bitmask {
	return m
}

// Equals returns true if both bitmasks are identical.
func (m *Bitmask) Equals(other Bitmask) bool {
	return m[0] == other[0] &&
		m[1] == other[1] &&
		m[2] == other[2] &&
		m[3] == other[3]
}

// IsDisjoint returns true if no bits are set in both m and other.
func (m *Bitmask) IsDisjoint(other Bitmask) bool {
	return !m.ContainsAny(other)
}
