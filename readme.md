# PECS

**Player Event Component System for Dragonfly**

PECS is a game architecture framework designed for [Dragonfly](https://github.com/df-mc/dragonfly) Minecraft servers. It provides structured game logic through handlers, components, systems, and dependency injection while respecting Dragonfly's transaction-based world model.

## Table of Contents

- [Installation](#installation)
- [Quick Start](#quick-start)
- [Core Concepts](#core-concepts)
  - [Sessions](#sessions)
  - [Fake Players & NPCs](#fake-players--npcs)
  - [Components](#components)
  - [Systems](#systems)
  - [Handlers](#handlers)
  - [Loops](#loops)
  - [Tasks](#tasks)
- [Dependency Injection](#dependency-injection)
  - [Tag Reference](#tag-reference)
  - [Phantom Types](#phantom-types)
  - [Resources](#resources)
- [Relations](#relations)
  - [Local Relations](#local-relations)
  - [Relation Resolution](#relation-resolution)
- [Federation](#federation)
  - [Overview](#federation-overview)
  - [Peer References](#peer-references)
  - [Shared References](#shared-references)
  - [Providers](#providers)
  - [When to Use Each Type](#when-to-use-each-type)
- [Events](#events)
- [Commands & Forms](#commands--forms)
- [Bundle Organization](#bundle-organization)
- [Execution Model](#execution-model)
  - [Stages](#stages)
  - [Parallelism](#parallelism)
  - [Transaction Context](#transaction-context)
- [Concurrency](#concurrency)
- [API Reference](#api-reference)
- [Acknowledgements](#acknowledgements)

---

## Installation

```bash
go get github.com/oriumgames/pecs
```

---

## Quick Start

```go
package main

import (
    "time"
    "github.com/df-mc/dragonfly/server"
    "github.com/df-mc/dragonfly/server/player"
    "github.com/oriumgames/pecs"
)

// Components are plain structs
type Health struct {
    Current int
    Max     int
}

// Handlers respond to player events
type DamageHandler struct {
    Session *pecs.Session
    Health  *Health `pecs:"mut"`
}

func (h *DamageHandler) HandleHurt(ev *pecs.EventHurt) {
    h.Health.Current -= int(*ev.Damage)
}

// Loops run at fixed intervals
type RegenLoop struct {
    Session *pecs.Session
    Health  *Health `pecs:"mut"`
}

func (l *RegenLoop) Run(tx *world.Tx) {
    if l.Health.Current < l.Health.Max {
        l.Health.Current++
    }
}

func main() {
    // Create a bundle for your game logic
    bundle := pecs.NewBundle("gameplay").
        Handler(&DamageHandler{}).
        Loop(&RegenLoop{}, time.Second, pecs.Default).
        Build()

    // Initialize PECS
    mngr := pecs.NewBuilder().
        Bundle(bundle).
        Init()

    // Start your Dragonfly server
    srv := server.New()
    srv.Listen()

    for p := range srv.Accept() {
        sess, err := mngr.NewSession(p)
        if err != nil {
            p.Disconnect("failed to initialize session")
            continue
        }

        pecs.Add(sess, &Health{Current: 20, Max: 20})
        p.Handle(pecs.NewHandler(sess, p))
    }
}
```

---

## Core Concepts

### Sessions

Sessions wrap Dragonfly players with persistent identity and component storage. They survive world transfers and provide thread-safe component access.

```go
// Create session when player joins
sess := mngr.NewSession(p)

// Retrieve session later
sess := mngr.GetSession(p)
sess := mngr.GetSessionByUUID(uuid)
sess := mngr.GetSessionByName("PlayerName")
sess := mngr.GetSessionByID(xuid)

// Get persistent identifier (XUID) - used for cross-server references
id := sess.ID()

// Access player within transaction context
if p, ok := sess.Player(tx); ok {
    p.Message("Hello!")
}

// Execute code in player's world transaction
sess.Exec(func(tx *world.Tx, p *player.Player) {
    p.Heal(10, healing.SourceFood{})
})

// Check session state
if sess.Closed() {
    return
}

// Get the current world
world := sess.World()

// Access manager from session
m := sess.Manager()
```

**Session Type Helpers:**

```go
// Check if session is a fake player (testing bot)
if sess.IsFake() {
    // Has FakeMarker component
}

// Check if session is an NPC entity
if sess.IsEntity() {
    // Has EntityMarker component
}

// Check if session is any kind of actor (fake or entity)
if sess.IsActor() {
    // Not a real player (no network session)
}
```

### Fake Players & NPCs

PECS supports spawning fake players (bots) and NPC entities that participate in the component system just like real players.

**Types:**

| Type | Marker | Federated ID | Use Case |
|------|--------|--------------|----------|
| Real Player | None | XUID | Human players |
| Fake Player | `FakeMarker` | Custom ID | Testing bots, lobby filling |
| NPC Entity | `EntityMarker` | None | AI entities, shopkeepers |

**Spawning Bots:**

```go
// Configuration for spawning
cfg := pecs.ActorConfig{
    Name:     "TestBot",
    Skin:     someSkin,
    Position: mgl64.Vec3{0, 64, 0},
    Yaw:      0,
    Pitch:    0,
}

// Spawn a fake player (can participate in Peer[T] lookups with fakeID)
sess := mngr.SpawnFake(tx, cfg, "fake-player-123")

// Spawn an NPC entity (local only, no federation)
sess := mngr.SpawnEntity(tx, cfg)

// Add components as usual
pecs.Add(sess, &Health{Current: 100, Max: 100})
```

**Federated ID Resolution:**

```go
// Real players: ID() returns XUID
// Fake players: ID() returns FakeMarker.ID
// Entities: ID() returns "" (no federated ID)
id := sess.ID()

// Unified lookup (works for real and fake players)
sess := mngr.GetSessionByID(id)
```

**Marker Components:**

```go
// FakeMarker and EntityMarker are empty struct markers
type FakeMarker struct{}
type EntityMarker struct{}

// Check with helper methods
if sess.IsFake() {
    fmt.Println("Fake player ID:", sess.ID())
}
```

### Components

Components are plain Go structs that hold data. No interfaces required.

```go
type Health struct {
    Current int
    Max     int
}

type Frozen struct {
    Until    time.Time
    FrozenBy string
}

type Inventory struct {
    Items []item.Stack
    Gold  int
}
```

**Component Operations:**

```go
// Add or replace component
pecs.Add(sess, &Health{Current: 20, Max: 20})

// Check existence
if pecs.Has[Health](sess) {
    // Has health component
}

// Get component (nil if missing)
if health := pecs.Get[Health](sess); health != nil {
    health.Current -= 5
}

// Get or add with default value
health := pecs.GetOrAdd(sess, &Health{Current: 100, Max: 100})

// Remove component
pecs.Remove[Frozen](sess)
```

**Lifecycle Hooks:**

Components can implement `Attachable` and/or `Detachable` for lifecycle callbacks:

```go
type Tracker struct {
    StartTime time.Time
}

func (t *Tracker) Attach(s *pecs.Session) {
    t.StartTime = time.Now()
    fmt.Println("Tracker attached to", s.Name())
}

func (t *Tracker) Detach(s *pecs.Session) {
    duration := time.Since(t.StartTime)
    fmt.Printf("Player %s tracked for %v\n", s.Name(), duration)
}
```

**Temporary Components:**

Components can be added with an expiration time. They are automatically removed when the time passes.

```go
// Add component that expires after duration
pecs.AddFor(sess, &SpeedBoost{Multiplier: 2.0}, 10*time.Second)

// Add component that expires at specific time
pecs.AddUntil(sess, &EventBuff{Bonus: 50}, eventEndTime)

// Check remaining time
remaining := pecs.ExpiresIn[SpeedBoost](sess)
if remaining > 0 {
    fmt.Printf("Speed boost expires in %v\n", remaining)
}

// Get exact expiration time
expireTime := pecs.ExpiresAt[SpeedBoost](sess)

// Check if expired (but not yet removed)
if pecs.Expired[SpeedBoost](sess) {
    // Will be removed next scheduler tick
}
```

Temporary components respect lifecycle hooks - `Detach` is called when the component expires.

### Systems

Systems contain game logic and declare dependencies via struct tags. PECS automatically injects the required data before execution.

There are three types of systems:
- **Handlers** - React to player events
- **Loops** - Run at fixed intervals
- **Tasks** - One-shot delayed execution

All systems share the same dependency injection model.

### Handlers

Handlers respond to events dispatched through PECS. They receive dependency injection just like loops and tasks.

```go
type DamageHandler struct {
    Session *pecs.Session
    Health  *Health  `pecs:"mut"`
    GodMode *GodMode `pecs:"opt"`
}

func (h *DamageHandler) HandleHurt(ev *pecs.EventHurt) {
    if h.GodMode != nil {
        *ev.Damage = 0
        return
    }
    h.Health.Current -= int(*ev.Damage)
    if h.Health.Current <= 0 {
        ev.Ctx.Val().Kill(ev.Source)
    }
}

func (h *DamageHandler) HandleDeath(ev *pecs.EventDeath) {
    ev.Player.Message("You died!")
}

// Register with bundle
bundle.Handler(&DamageHandler{})
```

**Built-in Events:**

PECS wraps all Dragonfly player events as pooled event types. See `event.go` for the complete list including `EventMove`, `EventHurt`, `EventDeath`, `EventChat`, `EventQuit`, and more.

**Custom Events:**

Handlers also support custom event types. See [Events](#events) for details on defining and dispatching your own events.

**Execution:**

Handlers execute synchronously in registration order. All matching handlers complete before `Dispatch()` returns.

### Loops

Loops run at fixed intervals for all sessions that match their component requirements. Loops without a `*pecs.Session` field or session components are global and run once per interval instead of per-session.

```go
type RegenLoop struct {
    Session *pecs.Session
    Health  *Health `pecs:"mut"`
    Config  *Config `pecs:"res"`
    
    _ pecs.Without[Combat]    // Don't regen while in combat
    _ pecs.Without[Spectator] // Spectators don't regen
}

func (l *RegenLoop) Run(tx *world.Tx) {
    if l.Health.Current < l.Health.Max {
        l.Health.Current += l.Config.RegenRate
        if l.Health.Current > l.Health.Max {
            l.Health.Current = l.Health.Max
        }
    }
}

// Register: runs every second in Default stage
bundle.Loop(&RegenLoop{}, time.Second, pecs.Default)

// Run every tick (interval of 0)
bundle.Loop(&TickLoop{}, 0, pecs.Before)

// Global loop (no session field or component) - runs once per interval
type WorldCleanupLoop struct {
    Manager *pecs.Manager
    Config  *ServerConfig `pecs:"res"`
}

func (l *WorldCleanupLoop) Run(tx *world.Tx) {
    for _, sess := range l.Manager.AllSessions() {
        // Clean up expired data...
    }
}

bundle.Loop(&WorldCleanupLoop{}, time.Minute, pecs.After)
```

### Tasks

Tasks are one-shot systems scheduled for future execution. Tasks without a `*pecs.Session` field or session components are global and can be scheduled with `ScheduleGlobal` or `DispatchGlobal`.

```go
type TeleportTask struct {
    Session *pecs.Session
    
    // Payload fields - set when scheduling
    Destination mgl64.Vec3
    Message     string
}

func (t *TeleportTask) Run(tx *world.Tx) {
    if p, ok := t.Session.Player(tx); ok {
        p.Teleport(t.Destination)
        p.Message(t.Message)
    }
}

// Register task type with bundle (enables pooling optimization)
bundle.Task(&TeleportTask{}, pecs.Default)

// Global task (no session field or component)
type ServerAnnouncementTask struct {
    Manager *pecs.Manager
    Message string
}

func (t *ServerAnnouncementTask) Run(tx *world.Tx) {
    t.Manager.MessageAll(tx, t.Message)
}
```

**Scheduling Tasks:**

```go
// Schedule for future execution
handle := pecs.Schedule(sess, &TeleportTask{
    Destination: mgl64.Vec3{0, 100, 0},
    Message:     "Welcome to spawn!",
}, 5*time.Second)

// Cancel if needed
handle.Cancel()

// Immediate dispatch (next tick)
pecs.Dispatch(sess, &SomeTask{})

// Schedule at specific time
pecs.ScheduleAt(sess, &DailyRewardTask{}, midnight)

// Repeating task (runs 5 times, every second)
repeatHandle := pecs.ScheduleRepeating(sess, &TickTask{}, time.Second, 5)

// Infinite repeating until cancelled
repeatHandle := pecs.ScheduleRepeating(sess, &HeartbeatTask{}, time.Second, -1)
repeatHandle.Cancel()

// Global tasks (not tied to any session)
pecs.ScheduleGlobal(mngr, &ServerAnnouncementTask{Message: "Restarting!"}, 5*time.Minute)
pecs.DispatchGlobal(mngr, &SomeGlobalTask{})
```

**Multi-Session Tasks:**

Tasks can involve two sessions (must be in the same world):

```go
type TradeTask struct {
    Session  *pecs.Session  // First player (buyer)
    Buyer    *Inventory `pecs:"mut"`
    
    Session2 *pecs.Session  // Second player (seller)
    Seller   *Inventory `pecs:"mut"`
    
    // Payload
    Item  item.Stack
    Price int
}

func (t *TradeTask) Run(tx *world.Tx) {
    // Both sessions guaranteed to be valid
    t.Seller.Items = append(t.Seller.Items, t.Item)
    t.Seller.Gold += t.Price
    t.Buyer.Gold -= t.Price
}

// Schedule multi-session task
pecs.Schedule2(buyer, seller, &TradeTask{Item: sword, Price: 100}, time.Second)
```

---

## Dependency Injection

### Tag Reference

| Tag | Description | Example |
|-----|-------------|---------|
| (none) | Required read-only component | `Health *Health` |
| `pecs:"mut"` | Required mutable component | `Health *Health \`pecs:"mut"\`` |
| `pecs:"opt"` | Optional component (nil if missing) | `Shield *Shield \`pecs:"opt"\`` |
| `pecs:"opt,mut"` | Optional mutable component | `Buff *Buff \`pecs:"opt,mut"\`` |
| `pecs:"rel"` | Relation traversal | `Target *Health \`pecs:"rel"\`` |
| `pecs:"res"` | Resource | `Config *Config \`pecs:"res"\`` |
| `pecs:"res,mut"` | Mutable resource | `State *State \`pecs:"res,mut"\`` |
| `pecs:"peer"` | Peer data resolution | `Friend *Profile \`pecs:"peer"\`` |
| `pecs:"shared"` | Shared entity resolution | `Party *PartyInfo \`pecs:"shared"\`` |

**Special Fields (auto-injected):**

| Field | Description |
|-------|-------------|
| `Session *pecs.Session` | Current session |
| `Manager *pecs.Manager` | Manager instance |

### Phantom Types

Use phantom types to filter which sessions a system runs on:

```go
type CombatLoop struct {
    Session *pecs.Session
    Health  *Health `pecs:"mut"`
    
    _ pecs.With[InCombat]     // Only run if InCombat component exists
    _ pecs.Without[Spectator] // Skip if Spectator component exists
    _ pecs.Without[Dead]      // Skip if Dead component exists
}
```

### Resources

Resources are global singletons available to all systems across all bundles. Register them with either the builder or a bundle:

```go
type GameConfig struct {
    MaxPartySize  int
    RegenInterval time.Duration
    SpawnPoint    mgl64.Vec3
}

type Database struct {
    conn *sql.DB
}

type Logger struct {
    prefix string
}

// Register globally with builder
mngr := pecs.NewBuilder().
    Resource(&Database{conn: db}).
    Resource(&Logger{prefix: "[PECS]"}).
    Bundle(gameBundle).
    Init()

// Or register with bundle (still globally accessible)
bundle.Resource(&GameConfig{
    MaxPartySize:  5,
    RegenInterval: time.Second,
    SpawnPoint:    mgl64.Vec3{0, 64, 0},
})

// Access in any system
type SaveHandler struct {
    Session *pecs.Session
    DB      *Database   `pecs:"res"`
    Logger  *Logger     `pecs:"res"`
    Config  *GameConfig `pecs:"res"`
}

func (h *SaveHandler) HandleQuit(ev *pecs.EventQuit) {
    h.Logger.Log("Player", ev.Player.Name(), "disconnecting")
    h.DB.SavePlayer(h.Session)
}

func (h *SaveHandler) HandleRespawn(ev *pecs.EventRespawn) {
    *ev.Position = h.Config.SpawnPoint
}
```

**Programmatic Access:**

```go
// From session
config := pecs.Resource[GameConfig](sess)

// From manager
db := pecs.ManagerResource[Database](mngr)
```

---

## Relations

Relations create type-safe links between sessions. PECS automatically cleans up relations when sessions disconnect.

### Local Relations

Use `Relation[T]` and `RelationSet[T]` for references between players on the same server:

```go
// Single reference
type Following struct {
    Target pecs.Relation[Position]
}

// Multiple references
type PartyLeader struct {
    Name    string
    Members pecs.RelationSet[PartyMember]
}

type PartyMember struct {
    JoinedAt time.Time
    Leader   pecs.Relation[PartyLeader]
}
```

**Using Relations:**

```go
// Set a relation
member := &PartyMember{JoinedAt: time.Now()}
member.Leader.Set(leaderSession)
pecs.Add(memberSession, member)

// Get target session
if targetSess := member.Leader.Get(); targetSess != nil {
    // Access target session
}

// Check validity
if member.Leader.Valid() {
    // Target exists and has the required component
}

// Clear relation
member.Leader.Clear()
```

**Using RelationSets:**

```go
leader := pecs.Get[PartyLeader](leaderSession)

// Add member
leader.Members.Add(memberSession)

// Remove member
leader.Members.Remove(memberSession)

// Check membership
if leader.Members.Has(memberSession) {
    // Is a member
}

// Get count
count := leader.Members.Len()

// Get all non-closed sessions
for _, memberSess := range leader.Members.All() {
    // Process each member session
}

// Resolve all valid members with their components
for _, resolved := range leader.Members.Resolve() {
    sess := resolved.Session     // *Session
    member := resolved.Component // *PartyMember
    // Process member data
}

// Clear all
leader.Members.Clear()
```

### Relation Resolution

Use the `pecs:"rel"` tag to automatically resolve relations in systems:

```go
type PartyHealLoop struct {
    Session *pecs.Session
    Member  *PartyMember
    Leader  *PartyLeader `pecs:"rel"` // Resolved from Member.Leader
}

func (l *PartyHealLoop) Run(tx *world.Tx) {
    if l.Leader != nil {
        // Access leader's data
        fmt.Println("Leader:", l.Leader.Name)
    }
}
```

For relation sets, inject a slice:

```go
type PartyBuffLoop struct {
    Session  *pecs.Session
    Leader   *PartyLeader
    Members  []*PartyMember `pecs:"rel"` // Resolved from Leader.Members
}

func (l *PartyBuffLoop) Run(tx *world.Tx) {
    for _, member := range l.Members {
        // Apply buff to each member
    }
}
```

**Manual Resolution:**

```go
// Resolve relation to get session and component
if sess, comp, ok := member.Leader.Resolve(); ok {
    fmt.Println("Leader name:", comp.Name)
}
```

---

## Federation

Federation enables cross-server data access. When players can be on different servers (in a network), you need a way to access their data regardless of which server they're on.

### Federation Overview

PECS provides two reference types for cross-server data:

| Type | Purpose | Target | Example |
|------|---------|--------|---------|
| `Peer[T]` | Reference another player's data | Player (by ID) | Friend, party member |
| `Shared[T]` | Reference shared entity data | Entity (by ID) | Party, guild, match |

Data is fetched via **Providers** - interfaces you implement to connect PECS to your backend services.

### Peer References

`Peer[T]` references another player's component data. Works whether the player is local or remote.

```go
// Component with peer reference
type FriendsList struct {
    BestFriend Peer[FriendProfile]   // Single friend
    Friends    PeerSet[FriendProfile] // Multiple friends
}

type FriendProfile struct {
    Username string
    Online   bool
    Server   string
}
```

**Using Peer:**

```go
// Set peer by player ID (e.g., XUID)
friends := &FriendsList{}
friends.BestFriend.Set("player-123-xuid")
pecs.Add(sess, friends)

// Get ID
id := friends.BestFriend.ID()

// Check if set
if friends.BestFriend.IsSet() {
    // Has a best friend set
}

// Clear
friends.BestFriend.Clear()

// Manual resolution (useful in commands/forms)
if profile, ok := friends.BestFriend.Resolve(sess.Manager()); ok {
    fmt.Println("Best friend:", profile.Username)
}
```

**Using PeerSet:**

```go
// Set all IDs
friends.Friends.Set([]string{"player-1", "player-2", "player-3"})

// Add single
friends.Friends.Add("player-4")

// Remove
friends.Friends.Remove("player-2")

// Get all IDs
ids := friends.Friends.IDs()

// Get count
count := friends.Friends.Len()

// Clear all
friends.Friends.Clear()

// Manual resolution (useful in commands/forms)
profiles := friends.Friends.Resolve(sess.Manager())
for _, profile := range profiles {
    fmt.Println("Friend:", profile.Username)
}
```

**Resolving Peer Data in Systems:**

```go
type FriendsDisplayLoop struct {
    Session     *pecs.Session
    FriendsList *FriendsList
    
    // PECS resolves these automatically via providers
    BestFriend  *FriendProfile   `pecs:"peer"` // From FriendsList.BestFriend
    AllFriends  []*FriendProfile `pecs:"peer"` // From FriendsList.Friends
}

func (l *FriendsDisplayLoop) Run(tx *world.Tx) {
    p, _ := l.Session.Player(tx)
    
    if l.BestFriend != nil {
        status := "offline"
        if l.BestFriend.Online {
            status = "online on " + l.BestFriend.Server
        }
        p.Message("Best friend: " + l.BestFriend.Username + " (" + status + ")")
    }
    
    for _, friend := range l.AllFriends {
        // Display friend info
    }
}
```

### Shared References

`Shared[T]` references shared entities (parties, guilds, matches) that aren't tied to a specific player.

```go
// Component with shared reference
type MatchmakingData struct {
    CurrentParty Shared[PartyInfo]
    ActiveMatch  Shared[MatchInfo]
}

type PartyInfo struct {
    ID       string
    LeaderID string
    Members  []PartyMemberInfo
    Open     bool
}

type PartyMemberInfo struct {
    ID       string
    Username string
}
```

**Using Shared:**

```go
mmData := &MatchmakingData{}
mmData.CurrentParty.Set("party-456")
pecs.Add(sess, mmData)

// Same API as Peer
id := mmData.CurrentParty.ID()
mmData.CurrentParty.Clear()

// Manual resolution (useful in commands/forms)
if party, ok := mmData.CurrentParty.Resolve(sess.Manager()); ok {
    fmt.Println("Party:", party.ID, "Members:", len(party.Members))
}
```

**Using SharedSet:**

```go
type GuildData struct {
    ActiveWars SharedSet[WarInfo]
}

// Same API as PeerSet
guildData.ActiveWars.Set([]string{"war-1", "war-2"})
guildData.ActiveWars.Add("war-3")

// Manual resolution
wars := guildData.ActiveWars.Resolve(sess.Manager())
for _, war := range wars {
    fmt.Println("War:", war.ID)
}
```

**Resolving Shared Data in Systems:**

```go
type PartyDisplayHandler struct {
    Session *pecs.Session
    MMData  *MatchmakingData
    Party   *PartyInfo `pecs:"shared"` // Resolved from MMData.CurrentParty
}

func (h *PartyDisplayHandler) HandleJoin(ev *pecs.EventJoin) {
    if h.Party != nil {
        ev.Player.Message("You're in party: " + h.Party.ID)
        ev.Player.Message("Leader: " + h.Party.LeaderID)
        ev.Player.Message("Members: " + strconv.Itoa(len(h.Party.Members)))
    }
}
```

### Providers

Providers fetch and sync data from your backend services. Implement `PeerProvider` for peer data and `SharedProvider` for shared data.

**PeerProvider:**

```go
When a player joins, PECS automatically queries all registered `PeerProvider`s. If data is returned,
components are added to the session and kept in sync via subscriptions. This removes the need for
manual data fetching handlers.

You can mark a provider as required using `pecs.WithRequired(true)`. If a required provider fails to
fetch data during session creation, `NewSession` will return an error.

type PeerProvider interface {
    // Unique name for logging
    Name() string
    
    // Component types this provider handles
    PlayerComponents() []reflect.Type
    
    // Fetch single player's components
    FetchPlayer(ctx context.Context, playerID string) ([]any, error)
    
    // Batch fetch multiple players
    FetchPlayers(ctx context.Context, playerIDs []string) (map[string][]any, error)
    
    // Subscribe to real-time updates
    SubscribePlayer(ctx context.Context, playerID string, updates chan<- PlayerUpdate) (Subscription, error)
}
```

**Example PeerProvider Implementation:**

```go
type StatusProvider struct {
    statusClient statusv1.StatusServiceClient
    nats         *nats.Conn
}

func (p *StatusProvider) Name() string {
    return "status"
}

func (p *StatusProvider) PlayerComponents() []reflect.Type {
    return []reflect.Type{reflect.TypeOf(FriendProfile{})}
}

func (p *StatusProvider) FetchPlayer(ctx context.Context, playerID string) ([]any, error) {
    resp, err := p.statusClient.GetStatus(ctx, &statusv1.GetStatusRequest{PlayerId: playerID})
    if err != nil {
        return nil, err
    }
    
    return []any{
        &FriendProfile{
            Username: resp.Username,
            Online:   resp.Online,
            Server:   resp.GetServerId(),
        },
    }, nil
}

func (p *StatusProvider) FetchPlayers(ctx context.Context, playerIDs []string) (map[string][]any, error) {
    resp, err := p.statusClient.GetStatuses(ctx, &statusv1.GetStatusesRequest{PlayerIds: playerIDs})
    if err != nil {
        return nil, err
    }
    
    result := make(map[string][]any)
    for id, status := range resp.Statuses {
        result[id] = []any{
            &FriendProfile{
                Username: status.Username,
                Online:   status.Online,
                Server:   status.GetServerId(),
            },
        }
    }
    return result, nil
}

func (p *StatusProvider) SubscribePlayer(ctx context.Context, playerID string, updates chan<- pecs.PlayerUpdate) (pecs.Subscription, error) {
    sub, err := p.nats.Subscribe("status.player."+playerID, func(msg *nats.Msg) {
        // Parse event and send update
        var event statusv1.StatusChanged
        proto.Unmarshal(msg.Data, &event)
        
        updates <- pecs.PlayerUpdate{
            ComponentType: reflect.TypeOf(FriendProfile{}),
            Data: &FriendProfile{
                Username: event.Username,
                Online:   event.Online,
                Server:   event.ServerId,
            },
        }
    })
    if err != nil {
        return nil, err
    }
    
    return &natsSubscription{sub}, nil
}
```

**SharedProvider:**

```go
type SharedProvider interface {
    Name() string
    EntityComponents() []reflect.Type
    FetchEntity(ctx context.Context, entityID string) (any, error)
    FetchEntities(ctx context.Context, entityIDs []string) (map[string]any, error)
    SubscribeEntity(ctx context.Context, entityID string, updates chan<- any) (Subscription, error)
}
```

**Registering Providers:**

```go
mngr := pecs.NewBuilder().
    Bundle(gameBundle).
    PeerProvider(&StatusProvider{...}).
    PeerProvider(&ProfileProvider{...}).
    SharedProvider(&PartyProvider{...}).
    SharedProvider(&MatchProvider{...}).
    Init()

// Or with options
mngr := pecs.NewBuilder().
    PeerProvider(&StatusProvider{...}, 
        pecs.WithFetchTimeout(2000),      // 2 second timeout
        pecs.WithGracePeriod(60000),      // 60 second cache grace period
        pecs.WithStaleTimeout(300000),    // 5 minute stale timeout
    ).
    Init()
```

### When to Use Each Type

| Type | Use When | Example |
|------|----------|---------|
| `Relation[T]` | Target **must** be on same server | Combat target, follow target |
| `Peer[T]` | Target is a **player** (local or remote) | Friend, party member, enemy |
| `Shared[T]` | Target is a **shared entity** (not a player) | Party, guild, match, server |

**Decision Flow:**

```
Is the target a player?
├── YES: Could they be on a different server?
│   ├── YES → Peer[T]
│   └── NO (guaranteed same server) → Relation[T]
└── NO (party, guild, match, etc.) → Shared[T]
```

---

## Events

Dispatch custom events to handler systems.

**Define Events:**

```go
type DamageEvent struct {
    Amount int
    Source world.DamageSource
}

type LevelUpEvent struct {
    NewLevel int
    OldLevel int
}
```

**Handle Events:**

```go
type NotificationHandler struct {
    Session *pecs.Session
}

// Method names don't matter - matching is done by event type
func (h *NotificationHandler) HandleDamage(e *DamageEvent) {
    if p, ok := h.Session.Player(nil); ok {
        p.Message(fmt.Sprintf("Took %d damage!", e.Amount))
    }
}

func (h *NotificationHandler) HandleLevelUp(e *LevelUpEvent) {
    if p, ok := h.Session.Player(nil); ok {
        p.Message(fmt.Sprintf("Level up! %d -> %d", e.OldLevel, e.NewLevel))
    }
}
```

**Dispatch Events:**

```go
// To single session
sess.Dispatch(&DamageEvent{Amount: 5, Source: src})

// To all sessions
mngr.Broadcast(&LevelUpEvent{NewLevel: 10, OldLevel: 9})

// To all except some
mngr.BroadcastExcept(&ChatEvent{Message: "Hello"}, sender)
sess.BroadcastExcept(event, sess) // Exclude self
```

**Built-in Events:**

```go
// Dispatched when a component is added
type ComponentAttachEvent struct {
    ComponentType reflect.Type
}

// Dispatched when a component is removed
type ComponentDetachEvent struct {
    ComponentType reflect.Type
}
```

---

## Commands & Forms

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
    
    health.Current = min(health.Current+c.Amount, health.Max)
    out.Printf("Healed %d HP!", c.Amount)
}

// Register command with bundle
bundle.Command(cmd.New("heal", "Heal yourself", nil, HealCommand{}))
```

**Forms:**

```go
type SettingsForm struct {
    EnableNotifications bool
    Volume              int
}

func (f SettingsForm) Submit(sub form.Submitter, tx *world.Tx) {
    p, sess := pecs.Form(sub)
    if sess == nil {
        return
    }
    
    settings := pecs.GetOrAdd(sess, &Settings{})
    settings.Notifications = f.EnableNotifications
    settings.Volume = f.Volume
}
```

---

## Bundle Organization

Structure your game with multiple bundles. Bundle names are used in panic/error messages for easier debugging.

```go
func main() {
    core := pecs.NewBundle("core").
        Resource(&ServerConfig{}).
        Handler(&JoinHandler{}).
        Handler(&QuitHandler{}).
        Loop(&AutoSaveLoop{}, time.Minute, pecs.After).
        Build()

    combat := pecs.NewBundle("combat").
        Resource(&CombatConfig{}).
        Handler(&DamageHandler{}).
        Handler(&DeathHandler{}).
        Loop(&CombatTagLoop{}, time.Second, pecs.Default).
        Task(&RespawnTask{}, pecs.Default).
        Build()

    party := pecs.NewBundle("party").
        Resource(&PartyConfig{}).
        Handler(&PartyInviteHandler{}).
        Handler(&PartyChatHandler{}).
        Command(cmd.New("party", "Party commands", nil, PartyCommand{})).
        Build()

    economy := pecs.NewBundle("economy").
        Resource(&EconomyConfig{}).
        Handler(&ShopHandler{}).
        Command(cmd.New("balance", "Check balance", nil, BalanceCommand{})).
        Command(cmd.New("pay", "Pay another player", nil, PayCommand{})).
        Build()

    mngr := pecs.NewBuilder().
        Resource(&Database{}).
        Resource(&Logger{}).
        Bundle(core).
        Bundle(combat).
        Bundle(party).
        Bundle(economy).
        PeerProvider(&StatusProvider{}).
        SharedProvider(&PartyProvider{}).
        Init()
}
```

---

## Execution Model

### Stages

Control execution order with three stages:

```go
const (
    pecs.Before  // Runs first - input handling, pre-processing
    pecs.Default // Runs second - main game logic
    pecs.After   // Runs last - cleanup, synchronization, rendering
)

bundle.Loop(&InputHandler{}, 0, pecs.Before)
bundle.Loop(&GameLogic{}, 0, pecs.Default)
bundle.Loop(&NetworkSync{}, 0, pecs.After)
```

### Parallelism

PECS automatically parallelizes non-conflicting systems:

- Systems in **different stages** run sequentially (Before → Default → After)
- Systems in the **same stage** that access **different components** run in parallel
- Systems that **write** to the same component type are serialized

The scheduler analyzes component access patterns via tags:
- Read access (`Health *Health`) doesn't conflict with other reads
- Write access (`Health *Health \`pecs:"mut"\``) conflicts with any other access to that component

### Transaction Context

All systems receive a `*world.Tx` parameter. Your session's player is guaranteed to be valid in this transaction.

```go
func (l *MyLoop) Run(tx *world.Tx) {
    // Your session's player - always valid
    p, _ := l.Session.Player(tx)
    p.Message("Hello!")
    
    // Other players via relations - check these
    for _, memberSess := range l.Members {
        if member, ok := memberSess.Player(tx); ok {
            member.Message("Party message")
        }
    }
}
```

---

## Concurrency

| Context | Safe Operations |
|---------|-----------------|
| Handlers | Read/write components directly |
| Loops | Read/write components directly |
| Tasks | Read/write components directly |
| Commands | Read/write components directly |
| Forms | Read/write components directly |
| External goroutines | **Must use `sess.Exec()`** |

**Critical Rule:** Never call `sess.Exec()` from within a handler, loop, task, command, or form when targeting a session in the same world. This causes deadlock. Use the existing transaction instead.

```go
// WRONG - potential deadlock
func (h *MyHandler) HandleChat(ev *pecs.EventChat) {
    otherSess.Exec(func(tx *world.Tx, p *player.Player) { // DEADLOCK!
        p.Message(*ev.Message)
    })
}

// CORRECT - use existing transaction context
func (h *MyHandler) HandleChat(ev *pecs.EventChat) {
    if other, ok := otherSess.Player(ev.Ctx.Val().Tx()); ok {
        other.Message(*ev.Message)
    }
}
```

---

## API Reference

### Manager

```go
// Session management
mngr.NewSession(p *player.Player) (*Session, error)
mngr.GetSessionByUUID(id uuid.UUID) *Session
mngr.GetSessionByName(name string) *Session
mngr.GetSessionByID(xuid string) *Session
mngr.GetSessionByHandle(h *world.EntityHandle) *Session
mngr.AllSessions() []*Session
mngr.AllSessionsInWorld(w *world.World) []*Session
mngr.SessionCount() int

// Events
mngr.Broadcast(event any)
mngr.BroadcastExcept(event any, exclude ...*Session)

// Federation
mngr.RegisterPeerProvider(p PeerProvider, opts ...ProviderOption)
mngr.RegisterSharedProvider(p SharedProvider, opts ...ProviderOption)

// Lifecycle
mngr.Shutdown()
mngr.TickNumber() uint64

// Spawn
SpawnFake(tx *world.Tx, cfg ActorConfig, fakeID string) *Session
SpawnEntity(tx *world.Tx, cfg ActorConfig) *Session
```

### Session

```go
sess.Handle() *world.EntityHandle
sess.UUID() uuid.UUID
sess.Name() string
sess.XUID() string
sess.ID() string // Same as XUID, for federation
sess.Player(tx *world.Tx) (*player.Player, bool)
sess.Exec(fn func(tx *world.Tx, p *player.Player)) bool
sess.World() *world.World
sess.Manager() *Manager
sess.Closed() bool
sess.Mask() Bitmask

// Events
sess.Dispatch(event any)
sess.Broadcast(event any)
sess.BroadcastExcept(event any, exclude ...*Session)
```

### Components

```go
pecs.Add[T any](s *Session, component *T)
pecs.Remove[T any](s *Session)
pecs.Get[T any](s *Session) *T
pecs.GetOrAdd[T any](s *Session, defaultVal *T) *T
pecs.Has[T any](s *Session) bool
```

### Tasks

```go
pecs.Schedule(s *Session, task Runnable, delay time.Duration) *TaskHandle
pecs.Schedule2(s1, s2 *Session, task Runnable, delay time.Duration) *TaskHandle
pecs.ScheduleAt(s *Session, task Runnable, at time.Time) *TaskHandle
pecs.ScheduleRepeating(s *Session, task Runnable, interval time.Duration, times int) *RepeatingTaskHandle
pecs.ScheduleGlobal(m *Manager, task Runnable, delay time.Duration) *TaskHandle
pecs.Dispatch(s *Session, task Runnable) *TaskHandle
pecs.Dispatch2(s1, s2 *Session, task Runnable) *TaskHandle
pecs.DispatchGlobal(s *Session, task Runnable) *TaskHandle

handle.Cancel()
repeatHandle.Cancel()
```

### Relations

```go
// Relation[T]
relation.Set(target *Session)
relation.Get() *Session
relation.Clear()
relation.Valid() bool
relation.Resolve() (sess *Session, comp *T, ok bool)

// RelationSet[T]
set.Add(target *Session)
set.Remove(target *Session)
set.Has(target *Session) bool
set.Clear()
set.Len() int
set.All() []*Session
set.Resolve() []Resolved[T]
```

### Peer & Shared

```go
// Peer[T]
peer.Set(playerID string)
peer.ID() string
peer.IsSet() bool
peer.Clear()
peer.Resolve(m *Manager) (*T, bool)

// PeerSet[T]
peerSet.Set(playerIDs []string)
peerSet.Add(playerID string)
peerSet.Remove(playerID string)
peerSet.IDs() []string
peerSet.Len() int
peerSet.Clear()
peerSet.Resolve(m *Manager) []*T

// Shared[T]
shared.Set(entityID string)
shared.ID() string
shared.IsSet() bool
shared.Clear()
shared.Resolve(m *Manager) (*T, bool)

// SharedSet[T]
sharedSet.Set(entityIDs []string)
sharedSet.Add(entityID string)
sharedSet.Remove(entityID string)
sharedSet.IDs() []string
sharedSet.Len() int
sharedSet.Clear()
sharedSet.Resolve(m *Manager) []*T
```

### Helpers

```go
pecs.Command(src cmd.Source) (*player.Player, *Session)
pecs.Form(sub form.Submitter) (*player.Player, *Session)
pecs.Item(user item.User) (*player.Player, *Session)
pecs.MustCommand(src cmd.Source) (*player.Player, *Session)
pecs.MustForm(sub form.Submitter) (*player.Player, *Session)
pecs.NewHandler(sess *Session, p *player.Player) Handler
```

- `pecs.Resource[T](sess)`: Retrieve a global resource from a session.
- `pecs.ManagerResource[T](mngr)`: Retrieve a global resource from a manager.

### Builder

```go
pecs.NewBuilder() *Builder

builder.Bundle(callback func(*Manager) *Bundle) *Builder
builder.Resource(res any) *Builder
builder.Handler(h Handler) *Builder
builder.Loop(sys Runnable, interval time.Duration, stage Stage) *Builder
builder.Task(sys Runnable, stage Stage) *Builder
builder.Command(command cmd.Command) *Builder
builder.PeerProvider(p PeerProvider, opts ...ProviderOption) *Builder
builder.SharedProvider(p SharedProvider, opts ...ProviderOption) *Builder
builder.Init() *Manager
```

### Bundle

```go
pecs.NewBundle(name string) *Bundle

bundle.Name() string
bundle.Resource(res any) *Bundle
bundle.Handler(h Handler) *Bundle
bundle.Loop(sys Runnable, interval time.Duration, stage Stage) *Bundle
bundle.Task(sys Runnable, stage Stage) *Bundle
bundle.Command(command cmd.Command) *Bundle
bundle.Build() func(*Manager) *Bundle
```

### Provider Options

```go
pecs.WithFetchTimeout(ms int64) ProviderOption   // Default: 5000ms
pecs.WithGracePeriod(ms int64) ProviderOption    // Default: 30000ms
pecs.WithStaleTimeout(ms int64) ProviderOption   // Default: 300000ms
```

---

## Acknowledgements

This work is inspired by [andreashgk/peex](https://github.com/andreashgk/peex).
