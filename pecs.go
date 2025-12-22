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
//	pecs:"peer"    Peer[T] resolution (remote player data)
//	pecs:"shared"  Shared[T] resolution (shared entity data)
package pecs

import "github.com/df-mc/dragonfly/server/world"

// Version is the PECS version.
const Version = "1.0.0"

// Runnable is the interface implemented by loops and tasks.
// The Run method contains the system's logic and is called when the system executes.
// The tx parameter is the active world transaction - use it instead of opening new transactions
// to avoid deadlocks. All sessions in a system are guaranteed to be in this transaction's world.
type Runnable interface {
	Run(tx *world.Tx)
}
