package pecs

// Runnable is the interface implemented by loops and tasks.
// The Run method contains the system's logic and is called when the system executes.
type Runnable interface {
	Run()
}
