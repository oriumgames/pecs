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

// EventMove is emitted when a player moves.
type EventMove struct {
	Ctx      *player.Context
	Position mgl64.Vec3
	Rotation cube.Rotation
}

func (e *EventMove) Cancel() { e.Ctx.Cancel() }

// EventJump is emitted when a player jumps.
type EventJump struct {
	Player *player.Player
}

// EventTeleport is emitted when a player is teleported.
type EventTeleport struct {
	Ctx      *player.Context
	Position mgl64.Vec3
}

func (e *EventTeleport) Cancel() { e.Ctx.Cancel() }

// EventChangeWorld is emitted when a player changes worlds.
type EventChangeWorld struct {
	Player *player.Player
	Before *world.World
	After  *world.World
}

// EventToggleSprint is emitted when a player toggles sprinting.
type EventToggleSprint struct {
	Ctx   *player.Context
	After bool
}

func (e *EventToggleSprint) Cancel() { e.Ctx.Cancel() }

// EventToggleSneak is emitted when a player toggles sneaking.
type EventToggleSneak struct {
	Ctx   *player.Context
	After bool
}

func (e *EventToggleSneak) Cancel() { e.Ctx.Cancel() }

// EventChat is emitted when a player sends a chat message.
type EventChat struct {
	Ctx     *player.Context
	Message *string
}

func (e *EventChat) Cancel() { e.Ctx.Cancel() }

// EventFoodLoss is emitted when a player loses food.
type EventFoodLoss struct {
	Ctx  *player.Context
	From int
	To   *int
}

func (e *EventFoodLoss) Cancel() { e.Ctx.Cancel() }

// EventHeal is emitted when a player is healed.
type EventHeal struct {
	Ctx    *player.Context
	Health *float64
	Source world.HealingSource
}

func (e *EventHeal) Cancel() { e.Ctx.Cancel() }

// EventHurt is emitted when a player is hurt.
type EventHurt struct {
	Ctx      *player.Context
	Damage   *float64
	Immune   bool
	Immunity *time.Duration
	Source   world.DamageSource
}

func (e *EventHurt) Cancel() { e.Ctx.Cancel() }

// EventDeath is emitted when a player dies.
type EventDeath struct {
	Player        *player.Player
	Source        world.DamageSource
	KeepInventory *bool
}

// EventRespawn is emitted when a player respawns.
type EventRespawn struct {
	Player   *player.Player
	Position *mgl64.Vec3
	World    **world.World
}

// EventSkinChange is emitted when a player changes their skin.
type EventSkinChange struct {
	Ctx  *player.Context
	Skin *skin.Skin
}

func (e *EventSkinChange) Cancel() { e.Ctx.Cancel() }

// EventFireExtinguish is emitted when a player extinguishes fire.
type EventFireExtinguish struct {
	Ctx      *player.Context
	Position cube.Pos
}

func (e *EventFireExtinguish) Cancel() { e.Ctx.Cancel() }

// EventStartBreak is emitted when a player starts breaking a block.
type EventStartBreak struct {
	Ctx      *player.Context
	Position cube.Pos
}

func (e *EventStartBreak) Cancel() { e.Ctx.Cancel() }

// EventBlockBreak is emitted when a player breaks a block.
type EventBlockBreak struct {
	Ctx        *player.Context
	Position   cube.Pos
	Drops      *[]item.Stack
	Experience *int
}

func (e *EventBlockBreak) Cancel() { e.Ctx.Cancel() }

// EventBlockPlace is emitted when a player places a block.
type EventBlockPlace struct {
	Ctx      *player.Context
	Position cube.Pos
	Block    world.Block
}

func (e *EventBlockPlace) Cancel() { e.Ctx.Cancel() }

// EventBlockPick is emitted when a player picks a block.
type EventBlockPick struct {
	Ctx      *player.Context
	Position cube.Pos
	Block    world.Block
}

func (e *EventBlockPick) Cancel() { e.Ctx.Cancel() }

// EventItemUse is emitted when a player uses an item.
type EventItemUse struct {
	Ctx *player.Context
}

func (e *EventItemUse) Cancel() { e.Ctx.Cancel() }

// EventItemUseOnBlock is emitted when a player uses an item on a block.
type EventItemUseOnBlock struct {
	Ctx      *player.Context
	Position cube.Pos
	Face     cube.Face
	ClickPos mgl64.Vec3
}

func (e *EventItemUseOnBlock) Cancel() { e.Ctx.Cancel() }

// EventItemUseOnEntity is emitted when a player uses an item on an entity.
type EventItemUseOnEntity struct {
	Ctx    *player.Context
	Entity world.Entity
}

func (e *EventItemUseOnEntity) Cancel() { e.Ctx.Cancel() }

// EventItemRelease is emitted when a player releases a charged item.
type EventItemRelease struct {
	Ctx      *player.Context
	Item     item.Stack
	Duration time.Duration
}

func (e *EventItemRelease) Cancel() { e.Ctx.Cancel() }

// EventItemConsume is emitted when a player consumes an item.
type EventItemConsume struct {
	Ctx  *player.Context
	Item item.Stack
}

func (e *EventItemConsume) Cancel() { e.Ctx.Cancel() }

// EventAttackEntity is emitted when a player attacks an entity.
type EventAttackEntity struct {
	Ctx      *player.Context
	Entity   world.Entity
	Force    *float64
	Height   *float64
	Critical *bool
}

func (e *EventAttackEntity) Cancel() { e.Ctx.Cancel() }

// EventExperienceGain is emitted when a player gains experience.
type EventExperienceGain struct {
	Ctx    *player.Context
	Amount *int
}

func (e *EventExperienceGain) Cancel() { e.Ctx.Cancel() }

// EventPunchAir is emitted when a player punches air.
type EventPunchAir struct {
	Ctx *player.Context
}

func (e *EventPunchAir) Cancel() { e.Ctx.Cancel() }

// EventSignEdit is emitted when a player edits a sign.
type EventSignEdit struct {
	Ctx       *player.Context
	Position  cube.Pos
	FrontSide bool
	OldText   string
	NewText   string
}

func (e *EventSignEdit) Cancel() { e.Ctx.Cancel() }

// EventLecternPageTurn is emitted when a player turns a lectern page.
type EventLecternPageTurn struct {
	Ctx      *player.Context
	Position cube.Pos
	OldPage  int
	NewPage  *int
}

func (e *EventLecternPageTurn) Cancel() { e.Ctx.Cancel() }

// EventItemDamage is emitted when an item takes damage.
type EventItemDamage struct {
	Ctx    *player.Context
	Item   item.Stack
	Damage *int
}

func (e *EventItemDamage) Cancel() { e.Ctx.Cancel() }

// EventItemPickup is emitted when a player picks up an item.
type EventItemPickup struct {
	Ctx  *player.Context
	Item *item.Stack
}

func (e *EventItemPickup) Cancel() { e.Ctx.Cancel() }

// EventHeldSlotChange is emitted when a player changes their held slot.
type EventHeldSlotChange struct {
	Ctx  *player.Context
	From int
	To   int
}

func (e *EventHeldSlotChange) Cancel() { e.Ctx.Cancel() }

// EventItemDrop is emitted when a player drops an item.
type EventItemDrop struct {
	Ctx  *player.Context
	Item item.Stack
}

func (e *EventItemDrop) Cancel() { e.Ctx.Cancel() }

// EventTransfer is emitted when a player is transferred to another server.
type EventTransfer struct {
	Ctx     *player.Context
	Address *net.UDPAddr
}

func (e *EventTransfer) Cancel() { e.Ctx.Cancel() }

// EventCommandExecution is emitted when a player executes a command.
type EventCommandExecution struct {
	Ctx     *player.Context
	Command cmd.Command
	Args    []string
}

func (e *EventCommandExecution) Cancel() { e.Ctx.Cancel() }

// EventJoin is emitted when a player joins the server.
type EventJoin struct {
	Player *player.Player
}

// EventQuit is emitted when a player quits the server.
type EventQuit struct {
	Player *player.Player
}

// EventDiagnostics is emitted for diagnostics data.
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
