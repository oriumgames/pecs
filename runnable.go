package pecs

import "github.com/df-mc/dragonfly/server/world"

// Runnable is the interface implemented by loops and tasks.
// The Run method contains the system's logic and is called when the system executes.
// The tx parameter is the active world transaction - use it instead of opening new transactions
// to avoid deadlocks. All sessions in a system are guaranteed to be in this transaction's world.
type Runnable interface {
	Run(tx *world.Tx)
}
