package pecs

import (
	"net"
	"sync"
	"time"

	"github.com/df-mc/dragonfly/server/block/cube"
	"github.com/df-mc/dragonfly/server/cmd"
	"github.com/df-mc/dragonfly/server/item"
	"github.com/df-mc/dragonfly/server/player"
	"github.com/df-mc/dragonfly/server/player/skin"
	"github.com/df-mc/dragonfly/server/session"
	"github.com/df-mc/dragonfly/server/world"
	"github.com/go-gl/mathgl/mgl64"
)

// Event types wrap Dragonfly handler parameters.
// Using PECS events decouples your handlers from Dragonfly's signature changes.

// EventMove is dispatched when a player moves.
type EventMove struct {
	Ctx      *player.Context
	Position mgl64.Vec3
	Rotation cube.Rotation
}

// EventJump is dispatched when a player jumps.
type EventJump struct {
	Player *player.Player
}

// EventTeleport is dispatched when a player is teleported.
type EventTeleport struct {
	Ctx      *player.Context
	Position mgl64.Vec3
}

// EventChangeWorld is dispatched when a player changes worlds.
type EventChangeWorld struct {
	Player *player.Player
	Before *world.World
	After  *world.World
}

// EventToggleSprint is dispatched when a player toggles sprinting.
type EventToggleSprint struct {
	Ctx   *player.Context
	After bool
}

// EventToggleSneak is dispatched when a player toggles sneaking.
type EventToggleSneak struct {
	Ctx   *player.Context
	After bool
}

// EventChat is dispatched when a player sends a chat message.
type EventChat struct {
	Ctx     *player.Context
	Message *string
}

// EventFoodLoss is dispatched when a player loses food.
type EventFoodLoss struct {
	Ctx  *player.Context
	From int
	To   *int
}

// EventHeal is dispatched when a player is healed.
type EventHeal struct {
	Ctx    *player.Context
	Health *float64
	Source world.HealingSource
}

// EventHurt is dispatched when a player is hurt.
type EventHurt struct {
	Ctx      *player.Context
	Damage   *float64
	Immune   bool
	Immunity *time.Duration
	Source   world.DamageSource
}

// EventDeath is dispatched when a player dies.
type EventDeath struct {
	Player        *player.Player
	Source        world.DamageSource
	KeepInventory *bool
}

// EventRespawn is dispatched when a player respawns.
type EventRespawn struct {
	Player   *player.Player
	Position *mgl64.Vec3
	World    **world.World
}

// EventSkinChange is dispatched when a player changes their skin.
type EventSkinChange struct {
	Ctx  *player.Context
	Skin *skin.Skin
}

// EventFireExtinguish is dispatched when a player extinguishes fire.
type EventFireExtinguish struct {
	Ctx      *player.Context
	Position cube.Pos
}

// EventStartBreak is dispatched when a player starts breaking a block.
type EventStartBreak struct {
	Ctx      *player.Context
	Position cube.Pos
}

// EventBlockBreak is dispatched when a player breaks a block.
type EventBlockBreak struct {
	Ctx        *player.Context
	Position   cube.Pos
	Drops      *[]item.Stack
	Experience *int
}

// EventBlockPlace is dispatched when a player places a block.
type EventBlockPlace struct {
	Ctx      *player.Context
	Position cube.Pos
	Block    world.Block
}

// EventBlockPick is dispatched when a player picks a block.
type EventBlockPick struct {
	Ctx      *player.Context
	Position cube.Pos
	Block    world.Block
}

// EventItemUse is dispatched when a player uses an item.
type EventItemUse struct {
	Ctx *player.Context
}

// EventItemUseOnBlock is dispatched when a player uses an item on a block.
type EventItemUseOnBlock struct {
	Ctx      *player.Context
	Position cube.Pos
	Face     cube.Face
	ClickPos mgl64.Vec3
}

// EventItemUseOnEntity is dispatched when a player uses an item on an entity.
type EventItemUseOnEntity struct {
	Ctx    *player.Context
	Entity world.Entity
}

// EventItemRelease is dispatched when a player releases a charged item.
type EventItemRelease struct {
	Ctx      *player.Context
	Item     item.Stack
	Duration time.Duration
}

// EventItemConsume is dispatched when a player consumes an item.
type EventItemConsume struct {
	Ctx  *player.Context
	Item item.Stack
}

// EventAttackEntity is dispatched when a player attacks an entity.
type EventAttackEntity struct {
	Ctx      *player.Context
	Entity   world.Entity
	Force    *float64
	Height   *float64
	Critical *bool
}

// EventExperienceGain is dispatched when a player gains experience.
type EventExperienceGain struct {
	Ctx    *player.Context
	Amount *int
}

// EventPunchAir is dispatched when a player punches air.
type EventPunchAir struct {
	Ctx *player.Context
}

// EventSignEdit is dispatched when a player edits a sign.
type EventSignEdit struct {
	Ctx       *player.Context
	Position  cube.Pos
	FrontSide bool
	OldText   string
	NewText   string
}

// EventLecternPageTurn is dispatched when a player turns a lectern page.
type EventLecternPageTurn struct {
	Ctx      *player.Context
	Position cube.Pos
	OldPage  int
	NewPage  *int
}

// EventItemDamage is dispatched when an item takes damage.
type EventItemDamage struct {
	Ctx    *player.Context
	Item   item.Stack
	Damage *int
}

// EventItemPickup is dispatched when a player picks up an item.
type EventItemPickup struct {
	Ctx  *player.Context
	Item *item.Stack
}

// EventHeldSlotChange is dispatched when a player changes their held slot.
type EventHeldSlotChange struct {
	Ctx  *player.Context
	From int
	To   int
}

// EventItemDrop is dispatched when a player drops an item.
type EventItemDrop struct {
	Ctx  *player.Context
	Item item.Stack
}

// EventTransfer is dispatched when a player is transferred to another server.
type EventTransfer struct {
	Ctx     *player.Context
	Address *net.UDPAddr
}

// EventCommandExecution is dispatched when a player executes a command.
type EventCommandExecution struct {
	Ctx     *player.Context
	Command cmd.Command
	Args    []string
}

// EventJoin is dispatched when a player joins the server.
type EventJoin struct {
	Player *player.Player
}

// EventQuit is dispatched when a player quits the server.
type EventQuit struct {
	Player *player.Player
}

// EventDiagnostics is dispatched for diagnostics data.
type EventDiagnostics struct {
	Player      *player.Player
	Diagnostics session.Diagnostics
}

// Event pools to reduce GC pressure.
var (
	eventMovePool             = sync.Pool{New: func() any { return &EventMove{} }}
	eventJumpPool             = sync.Pool{New: func() any { return &EventJump{} }}
	eventTeleportPool         = sync.Pool{New: func() any { return &EventTeleport{} }}
	eventChangeWorldPool      = sync.Pool{New: func() any { return &EventChangeWorld{} }}
	eventToggleSprintPool     = sync.Pool{New: func() any { return &EventToggleSprint{} }}
	eventToggleSneakPool      = sync.Pool{New: func() any { return &EventToggleSneak{} }}
	eventChatPool             = sync.Pool{New: func() any { return &EventChat{} }}
	eventFoodLossPool         = sync.Pool{New: func() any { return &EventFoodLoss{} }}
	eventHealPool             = sync.Pool{New: func() any { return &EventHeal{} }}
	eventHurtPool             = sync.Pool{New: func() any { return &EventHurt{} }}
	eventDeathPool            = sync.Pool{New: func() any { return &EventDeath{} }}
	eventRespawnPool          = sync.Pool{New: func() any { return &EventRespawn{} }}
	eventSkinChangePool       = sync.Pool{New: func() any { return &EventSkinChange{} }}
	eventFireExtinguishPool   = sync.Pool{New: func() any { return &EventFireExtinguish{} }}
	eventStartBreakPool       = sync.Pool{New: func() any { return &EventStartBreak{} }}
	eventBlockBreakPool       = sync.Pool{New: func() any { return &EventBlockBreak{} }}
	eventBlockPlacePool       = sync.Pool{New: func() any { return &EventBlockPlace{} }}
	eventBlockPickPool        = sync.Pool{New: func() any { return &EventBlockPick{} }}
	eventItemUsePool          = sync.Pool{New: func() any { return &EventItemUse{} }}
	eventItemUseOnBlockPool   = sync.Pool{New: func() any { return &EventItemUseOnBlock{} }}
	eventItemUseOnEntityPool  = sync.Pool{New: func() any { return &EventItemUseOnEntity{} }}
	eventItemReleasePool      = sync.Pool{New: func() any { return &EventItemRelease{} }}
	eventItemConsumePool      = sync.Pool{New: func() any { return &EventItemConsume{} }}
	eventAttackEntityPool     = sync.Pool{New: func() any { return &EventAttackEntity{} }}
	eventExperienceGainPool   = sync.Pool{New: func() any { return &EventExperienceGain{} }}
	eventPunchAirPool         = sync.Pool{New: func() any { return &EventPunchAir{} }}
	eventSignEditPool         = sync.Pool{New: func() any { return &EventSignEdit{} }}
	eventLecternPageTurnPool  = sync.Pool{New: func() any { return &EventLecternPageTurn{} }}
	eventItemDamagePool       = sync.Pool{New: func() any { return &EventItemDamage{} }}
	eventItemPickupPool       = sync.Pool{New: func() any { return &EventItemPickup{} }}
	eventHeldSlotChangePool   = sync.Pool{New: func() any { return &EventHeldSlotChange{} }}
	eventItemDropPool         = sync.Pool{New: func() any { return &EventItemDrop{} }}
	eventTransferPool         = sync.Pool{New: func() any { return &EventTransfer{} }}
	eventCommandExecutionPool = sync.Pool{New: func() any { return &EventCommandExecution{} }}
	eventJoinPool             = sync.Pool{New: func() any { return &EventJoin{} }}
	eventQuitPool             = sync.Pool{New: func() any { return &EventQuit{} }}
	eventDiagnosticsPool      = sync.Pool{New: func() any { return &EventDiagnostics{} }}
)
