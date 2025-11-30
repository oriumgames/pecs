# pecs

pecs is a player event component system for [dragonfly](https://github.com/df-mc/dragonfly) servers.

## Installation

```bash
go get github.com/yourusername/pecs
```

## Core Concepts

### 1. Setup

Initialize PECS in your server's main function using the `Builder`.

```go
func main() {
    // Create a bundle for your feature
    gameplay := pecs.NewBundle("gameplay").
        Handler(&MyHandler{}).
        Loop(&RegenSystem{}, time.Second, pecs.Default)
        // Injection, Resource, Command and Task are also available.

    // Initialize manager
    pecs.NewBuilder().
        Bundle(gameplay).
        Init()

    // ... start dragonfly server ...
}
```

When players join, create a Session and register the handler:

```go
for p := range srv.Accept() {
    sess := pecs.NewSession(p)
    p.Handle(pecs.NewHandler(sess))
}
```

### 2. Components

Components are plain Go structs. They store data state.

```go
type Health struct {
    Current int
    Max     int
}

type Frozen struct {
    Until time.Time
}
```

**Management:**
```go
// Add or Replace
pecs.Add(sess, &Health{Current: 20, Max: 20})

// Check
if pecs.Has[Frozen](sess) { ... }

// Get
if h := pecs.Get[Health](sess); h != nil {
    h.Current -= 5
}

// Remove
pecs.Remove[Frozen](sess)
```

### 3. Systems

Systems contain logic. They are stateless structs that have dependencies injected into them.

**Tags Reference:**
*   (none): Required read-only component
*   `pecs:"mut"`: Required mutable component (write access)
*   `pecs:"opt"`: Optional component (nil if missing)
*   `pecs:"rel"`: Relation traversal (see Relations)
*   `pecs:"res"`: Shared Bundle Resource
*   `pecs:"inj"`: Global Injection (Database, Logger)

**Example Handler:**
```go
type DamageHandler struct {
    player.NopHandler
    Session *pecs.Session
    Health  *Health `pecs:"mut"`      // Must have Health
    GodMode *GodMode `pecs:"opt"`     // Might have GodMode
    _       pecs.Without[Spectator]   // Skip if Spectator
}

func (h *DamageHandler) HandleHurt(ctx *player.Context, damage *float64, ...) {
    if h.GodMode != nil {
        *damage = 0
        return
    }
    h.Health.Current -= int(*damage)
}
```

**Example Loop:**
```go
type RegenSystem struct {
    Session *pecs.Session
    Health  *Health `pecs:"mut"`
}

func (s *RegenSystem) Run() {
    if s.Health.Current < s.Health.Max {
        s.Health.Current++
    }
}
```

**Example Task:**
Tasks are systems that run once after a delay. Unlike Loops, they can hold stateful payload data passed during scheduling.

```go
type TeleportTask struct {
    Session *pecs.Session
    Pending *TeleportPending `pecs:"mut"` // Only runs if this component exists
    
    // Payload field (not injected, set when scheduling)
    TargetPos mgl64.Vec3
}

func (t *TeleportTask) Run() {
    // Modify components directly
    pecs.Remove[TeleportPending](t.Session)
    
    // Interact with the player/world
    t.Session.Exec(func(tx *world.Tx, p *player.Player) {
        p.Teleport(t.TargetPos)
    })
}
```

### 4. Relations

PECS provides type-safe links between sessions that automatically clean themselves up when a player disconnects.

```go
type PartyMember struct {
    Leader pecs.Relation[PartyLeader] // Points to a session with PartyLeader
}

type PartyLeader struct {
    Members pecs.RelationSet[PartyMember] // Set of sessions with PartyMember
}
```

**Usage:**
```go
// Accessing relation
if leaderSess, leaderComp, ok := pecs.Resolve(member.Leader); ok {
    // leaderSess is the *Session
    // leaderComp is the *PartyLeader component on that session
}
```

### 5. Events

Dispatch custom events to handlers.

```go
// Define event
type MyEvent struct {
    Value int
}

// Handle in system
func (h *MyHandler) OnMyEvent(e MyEvent) {
    fmt.Println(e.Value)
}

// Dispatch to single session
sess.Dispatch(MyEvent{Value: 1})

// Broadcast to all sessions
pecs.Broadcast(MyEvent{Value: 1})
```

### 6. Scheduling

Schedule tasks for future execution using `pecs.Schedule`.

```go
// Schedule a task with payload
handle := pecs.Schedule(sess, &TeleportTask{
    TargetPos: mgl64.Vec3{100, 64, 100},
}, 3 * time.Second)

// Tasks are cancellable
handle.Cancel()
```

## Concurrency Model

*   **Systems/Loops**: Executed by the Scheduler. Thread-safe. Parallelized where possible based on component Read/Write conflicts. Guaranteed to run inside a valid World transaction.
*   **Handlers**: Executed by Dragonfly. Thread-safe relative to the world.
*   **Commands/Forms**: Executed by Dragonfly. Safe to read/write components directly.
*   **External Goroutines**: If you access components from your own goroutines (e.g. database callback), you **must** use `sess.Exec`:

```go
go func() {
    // ... db work ...
    sess.Exec(func(tx *world.Tx, p *player.Player) {
        // Safe to modify components here
        pecs.Get[Stats](sess).Kills++
    })
}()
```

**Warning**: Do not call `sess.Exec` when a world transaction (`*world.Tx`) is already present (e.g. inside Commands, Forms or Handlers). Doing so will cause a deadlock.

## Acknowledgements
This work is inspired by
[andreashgk/peex](https://github.com/andreashgk/peex)
