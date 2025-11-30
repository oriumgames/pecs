package pecs

// Stage represents a scheduling stage for system execution.
// Systems are executed in stage order: Before → Default → After.
type Stage int

const (
	// Before stage runs first. Use for pre-processing, input handling,
	// and setup logic that other systems depend on.
	Before Stage = iota

	// Default stage runs second. Use for main game logic including
	// combat, movement, abilities, and most gameplay systems.
	Default

	// After stage runs last. Use for cleanup, synchronization,
	// statistics logging, and network state updates.
	After

	// stageCount is the total number of stages.
	stageCount
)

// String returns the string representation of the stage.
func (s Stage) String() string {
	switch s {
	case Before:
		return "Before"
	case Default:
		return "Default"
	case After:
		return "After"
	default:
		return "Unknown"
	}
}
