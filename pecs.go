// Package pecs provides a Player Entity Component System for Dragonfly servers.
//
// PECS is an architectural layer built on top of Dragonfly that provides:
//   - Session abstraction for persistent player identity
//   - Component-based data storage per player
//   - Declarative dependency injection via struct tags
//   - Type-safe relations between sessions
//   - Transaction-safe handlers, loops, and tasks
//   - Multi-instance support for running multiple servers in one process
//
// # Quick Start
//
// Initialize PECS in your server setup:
//
//	bundle := pecs.NewBundle("MyGame").
//	    Handler(&MyHandler{}).
//	    Loop(&MyLoop{}, time.Second, pecs.Default)
//
//	mngr := pecs.NewBuilder().
//	    Bundle(bundle).
//	    Init()
//
//	for p := range srv.Accept() {
//	    sess, err := mngr.NewSession(p)
//	    if err != nil {
//	        p.Disconnect("failed to initialize session")
//	        continue
//	    }
//	    pecs.Add(sess, &Health{Current: 100, Max: 100})
//	    p.Handle(pecs.NewHandler(sess, p))
//	}
//
// # Components
//
// Components are plain Go structs attached to sessions:
//
//	type Health struct {
//	    Current int
//	    Max     int
//	}
//
//	pecs.Add(sess, &Health{100, 100})
//	health := pecs.Get[Health](sess)
//	pecs.Remove[Health](sess)
//
// # Systems
//
// Systems declare dependencies via struct tags:
//
//	type MyHandler struct {
//	    player.NopHandler
//	    Session *pecs.Session
//	    Manager *pecs.Manager      // Optional: for broadcasting, lookups
//	    Health  *Health            // Required
//	    Shield  *Shield `pecs:"opt"` // Optional
//	    Config  *Config `pecs:"res"` // Resource
//	    _ pecs.Without[Spectator]  // Skip if Spectator exists
//	}
//
// # Tag Reference
//
//	(none)         Required read-only component
//	pecs:"mut"     Required mutable component
//	pecs:"opt"     Optional (nil if missing)
//	pecs:"opt,mut" Optional mutable
//	pecs:"rel"     Relation traversal
//	pecs:"res"     Bundle resource
//	pecs:"res,mut" Mutable resource
//	pecs:"inj"     Global injection
package pecs

// Version is the PECS version.
const Version = "1.0.0"

// Re-export types for convenient access.
type (
	// Session is re-exported for documentation visibility.
	// See the Session type for full documentation.
	SessionType = Session

	// Bundle is re-exported for documentation visibility.
	BundleType = Bundle

	// Builder is re-exported for documentation visibility.
	BuilderType = Builder

	// Manager is re-exported for documentation visibility.
	ManagerType = Manager
)

// Re-export phantom types.
type (
	// WithType is the With[T] phantom type for requiring components.
	WithType[T any] = With[T]

	// WithoutType is the Without[T] phantom type for excluding components.
	WithoutType[T any] = Without[T]
)

// Re-export relation types.
type (
	// RelationType is the Relation[T] type for single references.
	RelationType[T any] = Relation[T]

	// RelationSetType is the RelationSet[T] type for multiple references.
	RelationSetType[T any] = RelationSet[T]
)

// Re-export lifecycle interfaces.
type (
	// AttachableType is the Attachable interface.
	AttachableType = Attachable

	// DetachableType is the Detachable interface.
	DetachableType = Detachable
)

// Re-export Runnable interface.
type RunnableType = Runnable
