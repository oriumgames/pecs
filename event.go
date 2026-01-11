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

func (e *EventMove) Cancel()             { e.Ctx.Cancel() }
func (e *EventMove) Val() *player.Player { return e.Ctx.Val() }
func (e *EventMove) Tx() *world.Tx       { return e.Ctx.Val().Tx() }

// EventJump is emitted when a player jumps.
type EventJump struct {
	Player *player.Player
}

// EventTeleport is emitted when a player is teleported.
type EventTeleport struct {
	Ctx      *player.Context
	Position mgl64.Vec3
}

func (e *EventTeleport) Cancel()             { e.Ctx.Cancel() }
func (e *EventTeleport) Val() *player.Player { return e.Ctx.Val() }
func (e *EventTeleport) Tx() *world.Tx       { return e.Ctx.Val().Tx() }

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

func (e *EventToggleSprint) Cancel()             { e.Ctx.Cancel() }
func (e *EventToggleSprint) Val() *player.Player { return e.Ctx.Val() }
func (e *EventToggleSprint) Tx() *world.Tx       { return e.Ctx.Val().Tx() }

// EventToggleSneak is emitted when a player toggles sneaking.
type EventToggleSneak struct {
	Ctx   *player.Context
	After bool
}

func (e *EventToggleSneak) Cancel()             { e.Ctx.Cancel() }
func (e *EventToggleSneak) Val() *player.Player { return e.Ctx.Val() }
func (e *EventToggleSneak) Tx() *world.Tx       { return e.Ctx.Val().Tx() }

// EventChat is emitted when a player sends a chat message.
type EventChat struct {
	Ctx     *player.Context
	Message *string
}

func (e *EventChat) Cancel()             { e.Ctx.Cancel() }
func (e *EventChat) Val() *player.Player { return e.Ctx.Val() }
func (e *EventChat) Tx() *world.Tx       { return e.Ctx.Val().Tx() }

// EventFoodLoss is emitted when a player loses food.
type EventFoodLoss struct {
	Ctx  *player.Context
	From int
	To   *int
}

func (e *EventFoodLoss) Cancel()             { e.Ctx.Cancel() }
func (e *EventFoodLoss) Val() *player.Player { return e.Ctx.Val() }
func (e *EventFoodLoss) Tx() *world.Tx       { return e.Ctx.Val().Tx() }

// EventHeal is emitted when a player is healed.
type EventHeal struct {
	Ctx    *player.Context
	Health *float64
	Source world.HealingSource
}

func (e *EventHeal) Cancel()             { e.Ctx.Cancel() }
func (e *EventHeal) Val() *player.Player { return e.Ctx.Val() }
func (e *EventHeal) Tx() *world.Tx       { return e.Ctx.Val().Tx() }

// EventHurt is emitted when a player is hurt.
type EventHurt struct {
	Ctx      *player.Context
	Damage   *float64
	Immune   bool
	Immunity *time.Duration
	Source   world.DamageSource
}

func (e *EventHurt) Cancel()             { e.Ctx.Cancel() }
func (e *EventHurt) Val() *player.Player { return e.Ctx.Val() }
func (e *EventHurt) Tx() *world.Tx       { return e.Ctx.Val().Tx() }

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

func (e *EventSkinChange) Cancel()             { e.Ctx.Cancel() }
func (e *EventSkinChange) Val() *player.Player { return e.Ctx.Val() }
func (e *EventSkinChange) Tx() *world.Tx       { return e.Ctx.Val().Tx() }

// EventFireExtinguish is emitted when a player extinguishes fire.
type EventFireExtinguish struct {
	Ctx      *player.Context
	Position cube.Pos
}

func (e *EventFireExtinguish) Cancel()             { e.Ctx.Cancel() }
func (e *EventFireExtinguish) Val() *player.Player { return e.Ctx.Val() }
func (e *EventFireExtinguish) Tx() *world.Tx       { return e.Ctx.Val().Tx() }

// EventStartBreak is emitted when a player starts breaking a block.
type EventStartBreak struct {
	Ctx      *player.Context
	Position cube.Pos
}

func (e *EventStartBreak) Cancel()             { e.Ctx.Cancel() }
func (e *EventStartBreak) Val() *player.Player { return e.Ctx.Val() }
func (e *EventStartBreak) Tx() *world.Tx       { return e.Ctx.Val().Tx() }

// EventBlockBreak is emitted when a player breaks a block.
type EventBlockBreak struct {
	Ctx        *player.Context
	Position   cube.Pos
	Drops      *[]item.Stack
	Experience *int
}

func (e *EventBlockBreak) Cancel()             { e.Ctx.Cancel() }
func (e *EventBlockBreak) Val() *player.Player { return e.Ctx.Val() }
func (e *EventBlockBreak) Tx() *world.Tx       { return e.Ctx.Val().Tx() }

// EventBlockPlace is emitted when a player places a block.
type EventBlockPlace struct {
	Ctx      *player.Context
	Position cube.Pos
	Block    world.Block
}

func (e *EventBlockPlace) Cancel()             { e.Ctx.Cancel() }
func (e *EventBlockPlace) Val() *player.Player { return e.Ctx.Val() }
func (e *EventBlockPlace) Tx() *world.Tx       { return e.Ctx.Val().Tx() }

// EventBlockPick is emitted when a player picks a block.
type EventBlockPick struct {
	Ctx      *player.Context
	Position cube.Pos
	Block    world.Block
}

func (e *EventBlockPick) Cancel()             { e.Ctx.Cancel() }
func (e *EventBlockPick) Val() *player.Player { return e.Ctx.Val() }
func (e *EventBlockPick) Tx() *world.Tx       { return e.Ctx.Val().Tx() }

// EventItemUse is emitted when a player uses an item.
type EventItemUse struct {
	Ctx *player.Context
}

func (e *EventItemUse) Cancel()             { e.Ctx.Cancel() }
func (e *EventItemUse) Val() *player.Player { return e.Ctx.Val() }
func (e *EventItemUse) Tx() *world.Tx       { return e.Ctx.Val().Tx() }

// EventItemUseOnBlock is emitted when a player uses an item on a block.
type EventItemUseOnBlock struct {
	Ctx      *player.Context
	Position cube.Pos
	Face     cube.Face
	ClickPos mgl64.Vec3
}

func (e *EventItemUseOnBlock) Cancel()             { e.Ctx.Cancel() }
func (e *EventItemUseOnBlock) Val() *player.Player { return e.Ctx.Val() }
func (e *EventItemUseOnBlock) Tx() *world.Tx       { return e.Ctx.Val().Tx() }

// EventItemUseOnEntity is emitted when a player uses an item on an entity.
type EventItemUseOnEntity struct {
	Ctx    *player.Context
	Entity world.Entity
}

func (e *EventItemUseOnEntity) Cancel()             { e.Ctx.Cancel() }
func (e *EventItemUseOnEntity) Val() *player.Player { return e.Ctx.Val() }
func (e *EventItemUseOnEntity) Tx() *world.Tx       { return e.Ctx.Val().Tx() }

// EventItemRelease is emitted when a player releases a charged item.
type EventItemRelease struct {
	Ctx      *player.Context
	Item     item.Stack
	Duration time.Duration
}

func (e *EventItemRelease) Cancel()             { e.Ctx.Cancel() }
func (e *EventItemRelease) Val() *player.Player { return e.Ctx.Val() }
func (e *EventItemRelease) Tx() *world.Tx       { return e.Ctx.Val().Tx() }

// EventItemConsume is emitted when a player consumes an item.
type EventItemConsume struct {
	Ctx  *player.Context
	Item item.Stack
}

func (e *EventItemConsume) Cancel()             { e.Ctx.Cancel() }
func (e *EventItemConsume) Val() *player.Player { return e.Ctx.Val() }
func (e *EventItemConsume) Tx() *world.Tx       { return e.Ctx.Val().Tx() }

// EventAttackEntity is emitted when a player attacks an entity.
type EventAttackEntity struct {
	Ctx      *player.Context
	Entity   world.Entity
	Force    *float64
	Height   *float64
	Critical *bool
}

func (e *EventAttackEntity) Cancel()             { e.Ctx.Cancel() }
func (e *EventAttackEntity) Val() *player.Player { return e.Ctx.Val() }
func (e *EventAttackEntity) Tx() *world.Tx       { return e.Ctx.Val().Tx() }

// EventExperienceGain is emitted when a player gains experience.
type EventExperienceGain struct {
	Ctx    *player.Context
	Amount *int
}

func (e *EventExperienceGain) Cancel()             { e.Ctx.Cancel() }
func (e *EventExperienceGain) Val() *player.Player { return e.Ctx.Val() }
func (e *EventExperienceGain) Tx() *world.Tx       { return e.Ctx.Val().Tx() }

// EventPunchAir is emitted when a player punches air.
type EventPunchAir struct {
	Ctx *player.Context
}

func (e *EventPunchAir) Cancel()             { e.Ctx.Cancel() }
func (e *EventPunchAir) Val() *player.Player { return e.Ctx.Val() }
func (e *EventPunchAir) Tx() *world.Tx       { return e.Ctx.Val().Tx() }

// EventSignEdit is emitted when a player edits a sign.
type EventSignEdit struct {
	Ctx       *player.Context
	Position  cube.Pos
	FrontSide bool
	OldText   string
	NewText   string
}

func (e *EventSignEdit) Cancel()             { e.Ctx.Cancel() }
func (e *EventSignEdit) Val() *player.Player { return e.Ctx.Val() }
func (e *EventSignEdit) Tx() *world.Tx       { return e.Ctx.Val().Tx() }

// EventSleep is emitted when a player sleeps.
type EventSleep struct {
	Ctx          *player.Context
	SendReminder *bool
}

func (e *EventSleep) Cancel()             { e.Ctx.Cancel() }
func (e *EventSleep) Val() *player.Player { return e.Ctx.Val() }
func (e *EventSleep) Tx() *world.Tx       { return e.Ctx.Val().Tx() }

// EventLecternPageTurn is emitted when a player turns a lectern page.
type EventLecternPageTurn struct {
	Ctx      *player.Context
	Position cube.Pos
	OldPage  int
	NewPage  *int
}

func (e *EventLecternPageTurn) Cancel()             { e.Ctx.Cancel() }
func (e *EventLecternPageTurn) Val() *player.Player { return e.Ctx.Val() }
func (e *EventLecternPageTurn) Tx() *world.Tx       { return e.Ctx.Val().Tx() }

// EventItemDamage is emitted when an item takes damage.
type EventItemDamage struct {
	Ctx    *player.Context
	Item   item.Stack
	Damage *int
}

func (e *EventItemDamage) Cancel()             { e.Ctx.Cancel() }
func (e *EventItemDamage) Val() *player.Player { return e.Ctx.Val() }
func (e *EventItemDamage) Tx() *world.Tx       { return e.Ctx.Val().Tx() }

// EventItemPickup is emitted when a player picks up an item.
type EventItemPickup struct {
	Ctx  *player.Context
	Item *item.Stack
}

func (e *EventItemPickup) Cancel()             { e.Ctx.Cancel() }
func (e *EventItemPickup) Val() *player.Player { return e.Ctx.Val() }
func (e *EventItemPickup) Tx() *world.Tx       { return e.Ctx.Val().Tx() }

// EventHeldSlotChange is emitted when a player changes their held slot.
type EventHeldSlotChange struct {
	Ctx  *player.Context
	From int
	To   int
}

func (e *EventHeldSlotChange) Cancel()             { e.Ctx.Cancel() }
func (e *EventHeldSlotChange) Val() *player.Player { return e.Ctx.Val() }
func (e *EventHeldSlotChange) Tx() *world.Tx       { return e.Ctx.Val().Tx() }

// EventItemDrop is emitted when a player drops an item.
type EventItemDrop struct {
	Ctx  *player.Context
	Item item.Stack
}

func (e *EventItemDrop) Cancel()             { e.Ctx.Cancel() }
func (e *EventItemDrop) Val() *player.Player { return e.Ctx.Val() }
func (e *EventItemDrop) Tx() *world.Tx       { return e.Ctx.Val().Tx() }

// EventTransfer is emitted when a player is transferred to another server.
type EventTransfer struct {
	Ctx     *player.Context
	Address *net.UDPAddr
}

func (e *EventTransfer) Cancel()             { e.Ctx.Cancel() }
func (e *EventTransfer) Val() *player.Player { return e.Ctx.Val() }
func (e *EventTransfer) Tx() *world.Tx       { return e.Ctx.Val().Tx() }

// EventCommandExecution is emitted when a player executes a command.
type EventCommandExecution struct {
	Ctx     *player.Context
	Command cmd.Command
	Args    []string
}

func (e *EventCommandExecution) Cancel()             { e.Ctx.Cancel() }
func (e *EventCommandExecution) Val() *player.Player { return e.Ctx.Val() }
func (e *EventCommandExecution) Tx() *world.Tx       { return e.Ctx.Val().Tx() }

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
	eventSleepPool            = sync.Pool{New: func() any { return &EventSleep{} }}
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
