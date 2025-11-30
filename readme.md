# pecs

**Player Entity Component System for Dragonfly**

pecs is designed for [Dragonfly](https://github.com/df-mc/dragonfly) Minecraft servers. It provides a structured approach to game logic through handlers, components, systems, and dependency injection while respecting Dragonfly's transaction-based world model.

## Installation

```bash
go get github.com/oriumgames/pecs
```

## Quick Start

```go
package main

import (
    "time"
    "github.com/df-mc/dragonfly/server"
    "github.com/df-mc/dragonfly/server/player"
    "github.com/pecs-framework/pecs"
)

func main() {
    // Create a bundle for your game logic
    bundle := pecs.NewBundle("gameplay").
        Resource(&Config{RegenRate: 1}).
        Handler(&DamageHandler{}).
        Loop(&RegenLoop{}, time.Second, pecs.Default)
        // Injection, Command and Task are also available.

    // Initialize PECS
    pecs.NewBuilder().
        Bundle(bundle).
        Init()

    // Start your Dragonfly server
    srv := // start a new server here
    srv.Listen()
    srv.CloseOnProgramEnd()

    for p := range srv.Accept() {
        sess := pecs.NewSession(p)
        pecs.Add(sess, &Health{Current: 20, Max: 20})
        p.Handle(pecs.NewHandler(sess))
    }
}
```

## Core Concepts

### Sessions

Sessions wrap Dragonfly players with persistent identity and component storage. They survive world transfers and provide thread-safe component access.

```go
// Create session when player joins
sess := pecs.NewSession(p)

// Retrieve session later
sess := pecs.GetSession(p)
sess := pecs.GetSessionByUUID(uuid)

// Access player within transaction context
if p, ok := sess.Player(tx); ok {
    p.Message("Hello!")
}

// Execute code in player's world transaction
sess.Exec(func(tx *world.Tx, p *player.Player) {
    p.Heal(10, healing.SourceFood{})
})
```

### Components

Components are plain structs that hold data. No interfaces required.

```go
type Health struct {
    Current int
    Max     int
}

type Frozen struct {
    Until    time.Time
    FrozenBy *pecs.Session
}

type PartyLeader struct {
    Name    string
    Members pecs.RelationSet[PartyMember]
}
```

**Operations:**

```go
// Add or replace component
pecs.Add(sess, &Health{Current: 20, Max: 20})

// Check existence
if pecs.Has[Health](sess) { ... }

// Get component (nil if missing)
if health := pecs.Get[Health](sess); health != nil {
    health.Current -= 5
}

// Remove component
pecs.Remove[Frozen](sess)
```

**Lifecycle Hooks:**

```go
type Tracker struct {
    StartTime time.Time
}

func (t *Tracker) Attach(s *pecs.Session) {
    t.StartTime = time.Now()
    fmt.Println("Tracker attached")
}

func (t *Tracker) Detach(s *pecs.Session) {
    fmt.Printf("Tracked for %v\n", time.Since(t.StartTime))
}
```

### Systems

Systems contain logic and declare dependencies via struct tags. PECS automatically injects the required data.

**Tag Reference:**

| Tag | Description |
|-----|-------------|
| (none) | Required read-only component |
| `pecs:"mut"` | Required mutable component |
| `pecs:"opt"` | Optional component (nil if missing) |
| `pecs:"opt,mut"` | Optional mutable component |
| `pecs:"rel"` | Relation traversal |
| `pecs:"res"` | Bundle resource |
| `pecs:"res,mut"` | Mutable bundle resource |
| `pecs:"inj"` | Global injection |

**Phantom Types:**

```go
type MyHandler struct {
    player.NopHandler
    Session *pecs.Session
    
    Health *Health `pecs:"mut"`
    
    _ pecs.With[Premium]      // Only runs if Premium exists
    _ pecs.Without[Spectator] // Skip if Spectator exists
}
```

### Handlers

Handlers respond to Dragonfly player events. They embed `player.NopHandler` and override methods.

```go
type DamageHandler struct {
    player.NopHandler
    Session *pecs.Session
    Health  *Health  `pecs:"mut"`
    GodMode *GodMode `pecs:"opt"`
}

func (h *DamageHandler) HandleHurt(ctx *player.Context, dmg *float64, immune *bool, immunity *time.Duration, src world.DamageSource) {
    if h.GodMode != nil {
        *dmg = 0
        return
    }
    h.Health.Current -= int(*dmg)
    if h.Health.Current <= 0 {
        ctx.Val().Message("§cYou died!")
    }
}

// Register
bundle.Handler(&DamageHandler{})
```

### Loops

Loops run at fixed intervals for all matching sessions.

```go
type RegenLoop struct {
    Session *pecs.Session
    Health  *Health `pecs:"mut"`
    Config  *Config `pecs:"res"`
    
    _ pecs.Without[Combat] // Don't regen in combat
}

func (l *RegenLoop) Run() {
    if l.Health.Current < l.Health.Max {
        l.Health.Current += l.Config.RegenRate
        if l.Health.Current > l.Health.Max {
            l.Health.Current = l.Health.Max
        }
    }
}

// Register (runs every second, in Default stage)
bundle.Loop(&RegenLoop{}, time.Second, pecs.Default)
```

### Tasks

Tasks are one-shot delayed systems.

```go
type TeleportTask struct {
    Session *pecs.Session
    
    // Payload
    Destination mgl64.Vec3
    Message     string
}

func (t *TeleportTask) Run() {
    if p, ok := t.Session.Player(/* tx from context */); ok {
        p.Teleport(t.Destination)
        p.Message(t.Message)
    }
}

// Schedule for 5 seconds from now
handle := pecs.Schedule(sess, &TeleportTask{
    Destination: mgl64.Vec3{0, 100, 0},
    Message:     "§aWelcome to spawn!",
}, 5*time.Second)

// Cancel if needed
handle.Cancel()

// Immediate dispatch (next tick)
pecs.Dispatch(sess, &SomeTask{})
```

**Multi-Session Tasks:**

```go
type TradeTask struct {
    Session  *pecs.Session   // First player
    Offer1 item.Stack
    
    Session2 *pecs.Session   // Second player
    Offer2 item.Stack
}

handle := pecs.Schedule2(buyer, seller, &TradeTask{...}, 3*time.Second)
```

### Relations

Type-safe links between sessions with automatic cleanup on disconnect.

```go
type PartyMember struct {
    JoinedAt time.Time
    Leader   pecs.Relation[PartyLeader]    // Points to one session
}

type PartyLeader struct {
    Name    string
    Members pecs.RelationSet[PartyMember]  // Points to many sessions
}
```

**Usage:**

```go
// Set relation
member := &PartyMember{JoinedAt: time.Now()}
member.Leader.Set(leaderSess)
pecs.Add(memberSess, member)

// Add to relation set
leader := pecs.Get[PartyLeader](leaderSess)
leader.Members.Add(memberSess)

// Resolve relation (get session and component)
if leaderSess, leaderComp, ok := pecs.Resolve(member.Leader); ok {
    leaderComp.Name // Access leader's data
}

// Iterate relation set
for _, memberSess := range leader.Members.All() {
    memberComp := pecs.Get[PartyMember](memberSess)
    // ...
}

// Check and clear
if member.Leader.Valid() { ... }
member.Leader.Clear()
leader.Members.Remove(memberSess)
```

### Events

Dispatch custom events to handlers.

```go
// Define event
type DamageEvent struct {
    Amount int
    Source world.DamageSource
}

// Handle in system
func (h *MyHandler) OnDamageEvent(e DamageEvent) {
    fmt.Printf("Took %d damage\n", e.Amount)
}

// Dispatch to single session
sess.Dispatch(DamageEvent{Amount: 5, Source: src})

// Broadcast to all sessions
pecs.Broadcast(DamageEvent{Amount: 10, Source: src})
```

### Resources & Injections

**Resources** are bundle-scoped shared data:

```go
type GameConfig struct {
    MaxPartySize  int
    RegenInterval time.Duration
}

bundle.Resource(&GameConfig{
    MaxPartySize:  5,
    RegenInterval: time.Second,
})

// Access in systems
type MyLoop struct {
    Config *GameConfig `pecs:"res"`
}
```

**Injections** are global singletons:

```go
type Database struct { /* ... */ }
type Logger struct { /* ... */ }

pecs.NewBuilder().
    Injection(&Database{}).
    Injection(&Logger{}).
    Bundle(bundle).
    Init()

// Access in systems
type MyHandler struct {
    DB     *Database `pecs:"inj"`
    Logger *Logger   `pecs:"inj"`
}
```

### Commands & Forms

Helper functions for Dragonfly commands and forms:

```go
type HealCommand struct {
    Amount int `cmd:"amount"`
}

func (c HealCommand) Run(src cmd.Source, out *cmd.Output, tx *world.Tx) {
    p, sess := pecs.Command(src)
    if sess == nil {
        out.Error("Player-only command")
        return
    }
    
    health := pecs.Get[Health](sess)
    if health == nil {
        out.Error("You don't have health!")
        return
    }
    
    health.Current += c.Amount
    out.Printf("Healed %d HP!", c.Amount)
}

// Forms
func (f MyForm) Submit(sub form.Submitter, tx *world.Tx) {
    p, sess := pecs.Form(sub)
    if sess == nil {
        return
    }
    // Handle form submission...
}
```

**Getting other players in transactions:**

```go
// WRONG - causes deadlock if same world!
otherSess.Exec(func(tx *world.Tx, p *player.Player) {
    p.Message("Hello")
})

// CORRECT - use existing transaction
if otherPlayer, ok := otherSess.Player(tx); ok {
    otherPlayer.Message("Hello")
}
```

## Bundle Organization

Structure your code with multiple bundles:

```go
func main() {
    core := pecs.NewBundle("core").
        Resource(&ServerConfig{}).
        Handler(&JoinHandler{}).
        Loop(&AutoSave{}, time.Minute, pecs.After)

    combat := pecs.NewBundle("combat").
        Resource(&CombatConfig{}).
        Command(cmd.New("stats", "shows stats", nil, &StatsCommand{})).
        Handler(&DamageHandler{}).
        Loop(&CombatTagLoop{}, time.Second, pecs.Default)

    party := pecs.NewBundle("party").
        Resource(&PartyConfig{}).
        Handler(&PartyChatHandler{}).
        Task(&PartyDisbandTask{}, pecs.Default)

    pecs.NewBuilder().
        Injection(&Database{}).
        Injection(&Logger{}).
        Bundle(core).
        Bundle(combat).
        Bundle(party).
        Init()
}
```

## Execution Stages

Control execution order with stages:

```go
bundle.Loop(&InputSystem{}, 0, pecs.Before)   // Runs first
bundle.Loop(&GameLogic{}, 0, pecs.Default)    // Runs second  
bundle.Loop(&RenderSystem{}, 0, pecs.After)   // Runs last
```

## Scheduler & Parallelism

PECS automatically parallelizes non-conflicting systems:

- Systems in **different stages** run sequentially
- Systems in the **same stage** with non-overlapping component access run in parallel
- Systems that write to the same component type are serialized

The scheduler respects Dragonfly's world transaction model, ensuring all systems execute within proper transaction contexts.

## Concurrency Rules

| Context | Safe Operations |
|---------|-----------------|
| Handlers | Read/write components directly |
| Loops | Read/write components directly |
| Tasks | Read/write components directly |
| Commands | Read/write components directly |
| Forms | Read/write components directly |
| External goroutines | Must use `sess.Exec()` |

**Critical:** Never call `sess.Exec()` from within a handler, loop, task, command, or form when targeting a session in the same world. This causes deadlock. Use the existing transaction instead.

## API Reference

### Session Functions

```go
pecs.NewSession(p *player.Player) *Session
pecs.GetSession(p *player.Player) *Session
pecs.GetSessionByUUID(id uuid.UUID) *Session
pecs.GetSessionByHandle(h *world.EntityHandle) *Session
pecs.AllSessions() []*Session
pecs.SessionCount() int
```

### Component Functions

```go
pecs.Add[T any](s *Session, component *T)
pecs.Remove[T any](s *Session)
pecs.Get[T any](s *Session) *T
pecs.Has[T any](s *Session) bool
```

### Task Functions

```go
pecs.Schedule(s *Session, task Runnable, delay time.Duration) *TaskHandle
pecs.Schedule2(s1, s2 *Session, task Runnable, delay time.Duration) *TaskHandle
pecs.Dispatch(s *Session, task Runnable)
pecs.Dispatch2(s1, s2 *Session, task Runnable)
```

### Event Functions

```go
sess.Dispatch(event any)
pecs.Broadcast(event any)
```

### Relation Functions

```go
pecs.Resolve[T any](r Relation[T]) (*Session, *T, bool)
```

### Helper Functions

```go
pecs.Command(src cmd.Source) (*player.Player, *Session)
pecs.Form(sub form.Submitter) (*player.Player, *Session)
pecs.MustCommand(src cmd.Source) (*player.Player, *Session)
pecs.MustForm(sub form.Submitter) (*player.Player, *Session)
```

## Acknowledgements
This work is inspired by
[andreashgk/peex](https://github.com/andreashgk/peex)
