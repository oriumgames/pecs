package pecs

import (
	"fmt"
	"log/slog"
	"net"
	"reflect"
	"sync"
	"time"
	"unsafe"

	"github.com/df-mc/dragonfly/server/block/cube"
	"github.com/df-mc/dragonfly/server/cmd"
	"github.com/df-mc/dragonfly/server/item"
	"github.com/df-mc/dragonfly/server/player"
	"github.com/df-mc/dragonfly/server/player/skin"
	"github.com/df-mc/dragonfly/server/session"
	"github.com/df-mc/dragonfly/server/world"
	"github.com/go-gl/mathgl/mgl64"
)

// funcval mirrors the runtime's internal function value structure.
// A Go function variable is internally a *funcval.
// See: https://github.com/golang/go/blob/master/src/runtime/runtime2.go
type funcval struct {
	fn uintptr
}

func init() {
	// Verify funcval layout matches Go's internal representation.
	// If this panics on a future Go version, the internal layout changed
	// and the unsafe event emission in registerHandler needs updating.
	testAddr := uintptr(0xDEADBEEF)
	fv := &funcval{fn: testAddr}

	// Convert our funcval to a function value and back to verify round-trip
	fn := *(*func())(unsafe.Pointer(&fv))
	recovered := *(*uintptr)(unsafe.Pointer(&fn))

	if recovered != uintptr(unsafe.Pointer(fv)) {
		panic("pecs: funcval layout assumption violated - unsafe event emission will not work on this Go version")
	}
}

// emptyInterface is the header for an empty interface. It is used to
// extract the underlying data pointer from an interface value.
type emptyInterface struct {
	typ  unsafe.Pointer
	data unsafe.Pointer
}

// unsafeEventEmitter is a function that calls an event handler method
// using unsafe pointers to avoid reflection overhead. The handler and event
// pointers are the raw data pointers from their respective interface values.
type unsafeEventEmitter func(handler, event unsafe.Pointer)

// handlerMeta holds metadata and pool for a registered handler type.
type handlerMeta struct {
	meta   *SystemMeta
	bundle *Bundle
	// events is a map of event types to their unsafe, reflection-free emitter.
	events map[reflect.Type]unsafeEventEmitter
}

// Handlers receive events by implementing methods with a single pointer argument:
//
//	func (h *MyHandler) HandleHurt(ev *pecs.EventHurt)
//	func (h *MyHandler) HandleItemDrop(ev *pecs.EventItemDrop)
//
// Method names don't matter - matching is done by event type.
// Handlers are registered with bundles and receive dependency injection.

// SessionHandler wraps a PECS session to implement Dragonfly's player.Handler.
// It receives Dragonfly events and emits them as pooled PECS events.
//
// Concurrency:
// Handlers are executed synchronously by Dragonfly (typically within the world's
// tick loop or packet processing). This means they are serialized with respect
// to the world state and generally do not race with PECS Loops or Tasks, as
// those are also executed within the world's transaction context via the Scheduler.
//
// It is safe for Handlers to read/write Components, as they effectively have
// exclusive access during execution relative to the specific world.
type SessionHandler struct {
	session *Session
}

// Session returns the session associated with this handler.
func (h *SessionHandler) Session() *Session {
	return h.session
}

// NewHandler creates a new Dragonfly handler for the given session.
// This should be passed to player.Handle().
func NewHandler(s *Session, p *player.Player) player.Handler {
	h := &SessionHandler{session: s}
	// Emit join event
	ev := eventJoinPool.Get().(*EventJoin)
	ev.Player = p
	s.Emit(ev)
	*ev = EventJoin{}
	eventJoinPool.Put(ev)
	return h
}

// Compile-time check that SessionHandler implements player.Handler.
var _ player.Handler = (*SessionHandler)(nil)

// Emit sends an event to all registered handlers that listen for it.
// Handlers listen for events by implementing a method with the signature:
//
//	func (h *MyHandler) HandleMyEvent(ev *MyEventType)
//
// The method name does not matter, only the signature (one argument).
func (s *Session) Emit(event any) {
	if s.manager == nil || s.closed.Load() {
		return
	}

	eventType := reflect.TypeOf(event)
	if eventType.Kind() != reflect.Pointer {
		// Log warning for non-pointer events - these can't be emitted via unsafe path.
		// The registration path panics for handlers with value-type event parameters,
		// but this can still happen if Emit is called with a value directly.
		slog.Warn("pecs: emit ignored non-pointer event",
			"type", eventType.String(),
			"session", s.name)
		return
	}

	// Extract the raw data pointer from the event interface.
	eventPtr := (*emptyInterface)(unsafe.Pointer(&event)).data
	sessionSlice := [1]*Session{s}

	for _, hm := range s.manager.handlers {
		// Check if this handler handles this event type.
		emitter, ok := hm.events[eventType]
		if !ok {
			continue
		}

		// Check bitmask.
		if !s.canRun(hm.meta) {
			continue
		}

		// Get handler from pool.
		handler := hm.meta.Pool.Get()

		// Inject dependencies.
		if !injectSystem(handler, sessionSlice[:], hm.meta, hm.bundle, s.manager) {
			zeroSystem(handler, hm.meta)
			hm.meta.Pool.Put(handler)
			continue
		}

		// Extract the raw data pointer from the handler interface.
		handlerPtr := (*emptyInterface)(unsafe.Pointer(&handler)).data

		// Execute the pre-compiled unsafe emitter.
		emitter(handlerPtr, eventPtr)

		// Zero and return to pool.
		zeroSystem(handler, hm.meta)
		hm.meta.Pool.Put(handler)
	}
}

// ComponentAttachEvent is emitted when a component is added to a session.
type ComponentAttachEvent struct {
	ComponentType reflect.Type
}

// ComponentDetachEvent is emitted when a component is removed from a session.
type ComponentDetachEvent struct {
	ComponentType reflect.Type
}

// HandleMove handles the player moving.
func (h *SessionHandler) HandleMove(ctx *player.Context, newPos mgl64.Vec3, newRot cube.Rotation) {
	ev := eventMovePool.Get().(*EventMove)
	ev.Ctx = ctx
	ev.Position = newPos
	ev.Rotation = newRot
	h.session.Emit(ev)
	*ev = EventMove{}
	eventMovePool.Put(ev)
}

// HandleJump handles the player jumping.
func (h *SessionHandler) HandleJump(p *player.Player) {
	ev := eventJumpPool.Get().(*EventJump)
	ev.Player = p
	h.session.Emit(ev)
	*ev = EventJump{}
	eventJumpPool.Put(ev)
}

// HandleTeleport handles the player being teleported.
func (h *SessionHandler) HandleTeleport(ctx *player.Context, pos mgl64.Vec3) {
	ev := eventTeleportPool.Get().(*EventTeleport)
	ev.Ctx = ctx
	ev.Position = pos
	h.session.Emit(ev)
	*ev = EventTeleport{}
	eventTeleportPool.Put(ev)
}

// HandleChangeWorld handles the player changing worlds.
func (h *SessionHandler) HandleChangeWorld(p *player.Player, before, after *world.World) {
	h.session.updateWorldCache(after)
	if h.session.manager != nil {
		h.session.manager.MoveSession(h.session, before, after)
	}
	ev := eventChangeWorldPool.Get().(*EventChangeWorld)
	ev.Player = p
	ev.Before = before
	ev.After = after
	h.session.Emit(ev)
	*ev = EventChangeWorld{}
	eventChangeWorldPool.Put(ev)
}

// HandleToggleSprint handles the player toggling sprint.
func (h *SessionHandler) HandleToggleSprint(ctx *player.Context, after bool) {
	ev := eventToggleSprintPool.Get().(*EventToggleSprint)
	ev.Ctx = ctx
	ev.After = after
	h.session.Emit(ev)
	*ev = EventToggleSprint{}
	eventToggleSprintPool.Put(ev)
}

// HandleToggleSneak handles the player toggling sneak.
func (h *SessionHandler) HandleToggleSneak(ctx *player.Context, after bool) {
	ev := eventToggleSneakPool.Get().(*EventToggleSneak)
	ev.Ctx = ctx
	ev.After = after
	h.session.Emit(ev)
	*ev = EventToggleSneak{}
	eventToggleSneakPool.Put(ev)
}

// HandleChat handles the player sending a chat message.
func (h *SessionHandler) HandleChat(ctx *player.Context, message *string) {
	ev := eventChatPool.Get().(*EventChat)
	ev.Ctx = ctx
	ev.Message = message
	h.session.Emit(ev)
	*ev = EventChat{}
	eventChatPool.Put(ev)
}

// HandleFoodLoss handles the player losing food.
func (h *SessionHandler) HandleFoodLoss(ctx *player.Context, from int, to *int) {
	ev := eventFoodLossPool.Get().(*EventFoodLoss)
	ev.Ctx = ctx
	ev.From = from
	ev.To = to
	h.session.Emit(ev)
	*ev = EventFoodLoss{}
	eventFoodLossPool.Put(ev)
}

// HandleHeal handles the player being healed.
func (h *SessionHandler) HandleHeal(ctx *player.Context, health *float64, src world.HealingSource) {
	ev := eventHealPool.Get().(*EventHeal)
	ev.Ctx = ctx
	ev.Health = health
	ev.Source = src
	h.session.Emit(ev)
	*ev = EventHeal{}
	eventHealPool.Put(ev)
}

// HandleHurt handles the player being hurt.
func (h *SessionHandler) HandleHurt(ctx *player.Context, damage *float64, immune bool, attackImmunity *time.Duration, src world.DamageSource) {
	ev := eventHurtPool.Get().(*EventHurt)
	ev.Ctx = ctx
	ev.Damage = damage
	ev.Immune = immune
	ev.Immunity = attackImmunity
	ev.Source = src
	h.session.Emit(ev)
	*ev = EventHurt{}
	eventHurtPool.Put(ev)
}

// HandleDeath handles the player dying.
func (h *SessionHandler) HandleDeath(p *player.Player, src world.DamageSource, keepInv *bool) {
	ev := eventDeathPool.Get().(*EventDeath)
	ev.Player = p
	ev.Source = src
	ev.KeepInventory = keepInv
	h.session.Emit(ev)
	*ev = EventDeath{}
	eventDeathPool.Put(ev)
}

// HandleRespawn handles the player respawning.
func (h *SessionHandler) HandleRespawn(p *player.Player, pos *mgl64.Vec3, w **world.World) {
	ev := eventRespawnPool.Get().(*EventRespawn)
	ev.Player = p
	ev.Position = pos
	ev.World = w
	h.session.Emit(ev)
	*ev = EventRespawn{}
	eventRespawnPool.Put(ev)
}

// HandleSkinChange handles the player changing their skin.
func (h *SessionHandler) HandleSkinChange(ctx *player.Context, sk *skin.Skin) {
	ev := eventSkinChangePool.Get().(*EventSkinChange)
	ev.Ctx = ctx
	ev.Skin = sk
	h.session.Emit(ev)
	*ev = EventSkinChange{}
	eventSkinChangePool.Put(ev)
}

// HandleFireExtinguish handles the player extinguishing fire.
func (h *SessionHandler) HandleFireExtinguish(ctx *player.Context, pos cube.Pos) {
	ev := eventFireExtinguishPool.Get().(*EventFireExtinguish)
	ev.Ctx = ctx
	ev.Position = pos
	h.session.Emit(ev)
	*ev = EventFireExtinguish{}
	eventFireExtinguishPool.Put(ev)
}

// HandleStartBreak handles the player starting to break a block.
func (h *SessionHandler) HandleStartBreak(ctx *player.Context, pos cube.Pos) {
	ev := eventStartBreakPool.Get().(*EventStartBreak)
	ev.Ctx = ctx
	ev.Position = pos
	h.session.Emit(ev)
	*ev = EventStartBreak{}
	eventStartBreakPool.Put(ev)
}

// HandleBlockBreak handles block breaking.
func (h *SessionHandler) HandleBlockBreak(ctx *player.Context, pos cube.Pos, drops *[]item.Stack, xp *int) {
	ev := eventBlockBreakPool.Get().(*EventBlockBreak)
	ev.Ctx = ctx
	ev.Position = pos
	ev.Drops = drops
	ev.Experience = xp
	h.session.Emit(ev)
	*ev = EventBlockBreak{}
	eventBlockBreakPool.Put(ev)
}

// HandleBlockPlace handles block placement.
func (h *SessionHandler) HandleBlockPlace(ctx *player.Context, pos cube.Pos, b world.Block) {
	ev := eventBlockPlacePool.Get().(*EventBlockPlace)
	ev.Ctx = ctx
	ev.Position = pos
	ev.Block = b
	h.session.Emit(ev)
	*ev = EventBlockPlace{}
	eventBlockPlacePool.Put(ev)
}

// HandleBlockPick handles picking a block.
func (h *SessionHandler) HandleBlockPick(ctx *player.Context, pos cube.Pos, b world.Block) {
	ev := eventBlockPickPool.Get().(*EventBlockPick)
	ev.Ctx = ctx
	ev.Position = pos
	ev.Block = b
	h.session.Emit(ev)
	*ev = EventBlockPick{}
	eventBlockPickPool.Put(ev)
}

// HandleItemUse handles general item use.
func (h *SessionHandler) HandleItemUse(ctx *player.Context) {
	ev := eventItemUsePool.Get().(*EventItemUse)
	ev.Ctx = ctx
	h.session.Emit(ev)
	*ev = EventItemUse{}
	eventItemUsePool.Put(ev)
}

// HandleItemUseOnBlock handles using an item on a block.
func (h *SessionHandler) HandleItemUseOnBlock(ctx *player.Context, pos cube.Pos, face cube.Face, clickPos mgl64.Vec3) {
	ev := eventItemUseOnBlockPool.Get().(*EventItemUseOnBlock)
	ev.Ctx = ctx
	ev.Position = pos
	ev.Face = face
	ev.ClickPos = clickPos
	h.session.Emit(ev)
	*ev = EventItemUseOnBlock{}
	eventItemUseOnBlockPool.Put(ev)
}

// HandleItemUseOnEntity handles using an item on an entity.
func (h *SessionHandler) HandleItemUseOnEntity(ctx *player.Context, e world.Entity) {
	ev := eventItemUseOnEntityPool.Get().(*EventItemUseOnEntity)
	ev.Ctx = ctx
	ev.Entity = e
	h.session.Emit(ev)
	*ev = EventItemUseOnEntity{}
	eventItemUseOnEntityPool.Put(ev)
}

// HandleItemRelease handles releasing a charged-use item.
func (h *SessionHandler) HandleItemRelease(ctx *player.Context, it item.Stack, dur time.Duration) {
	ev := eventItemReleasePool.Get().(*EventItemRelease)
	ev.Ctx = ctx
	ev.Item = it
	ev.Duration = dur
	h.session.Emit(ev)
	*ev = EventItemRelease{}
	eventItemReleasePool.Put(ev)
}

// HandleItemConsume handles consuming an item.
func (h *SessionHandler) HandleItemConsume(ctx *player.Context, it item.Stack) {
	ev := eventItemConsumePool.Get().(*EventItemConsume)
	ev.Ctx = ctx
	ev.Item = it
	h.session.Emit(ev)
	*ev = EventItemConsume{}
	eventItemConsumePool.Put(ev)
}

// HandleAttackEntity handles attacking an entity.
func (h *SessionHandler) HandleAttackEntity(ctx *player.Context, e world.Entity, force, height *float64, critical *bool) {
	ev := eventAttackEntityPool.Get().(*EventAttackEntity)
	ev.Ctx = ctx
	ev.Entity = e
	ev.Force = force
	ev.Height = height
	ev.Critical = critical
	h.session.Emit(ev)
	*ev = EventAttackEntity{}
	eventAttackEntityPool.Put(ev)
}

// HandleExperienceGain handles XP gain.
func (h *SessionHandler) HandleExperienceGain(ctx *player.Context, amount *int) {
	ev := eventExperienceGainPool.Get().(*EventExperienceGain)
	ev.Ctx = ctx
	ev.Amount = amount
	h.session.Emit(ev)
	*ev = EventExperienceGain{}
	eventExperienceGainPool.Put(ev)
}

// HandlePunchAir handles punching air.
func (h *SessionHandler) HandlePunchAir(ctx *player.Context) {
	ev := eventPunchAirPool.Get().(*EventPunchAir)
	ev.Ctx = ctx
	h.session.Emit(ev)
	*ev = EventPunchAir{}
	eventPunchAirPool.Put(ev)
}

// HandleSignEdit handles sign text editing.
func (h *SessionHandler) HandleSignEdit(ctx *player.Context, pos cube.Pos, frontSide bool, oldText, newText string) {
	ev := eventSignEditPool.Get().(*EventSignEdit)
	ev.Ctx = ctx
	ev.Position = pos
	ev.FrontSide = frontSide
	ev.OldText = oldText
	ev.NewText = newText
	h.session.Emit(ev)
	*ev = EventSignEdit{}
	eventSignEditPool.Put(ev)
}

// HandleLecternPageTurn handles page turning on lecterns.
func (h *SessionHandler) HandleLecternPageTurn(ctx *player.Context, pos cube.Pos, oldPage int, newPage *int) {
	ev := eventLecternPageTurnPool.Get().(*EventLecternPageTurn)
	ev.Ctx = ctx
	ev.Position = pos
	ev.OldPage = oldPage
	ev.NewPage = newPage
	h.session.Emit(ev)
	*ev = EventLecternPageTurn{}
	eventLecternPageTurnPool.Put(ev)
}

// HandleItemDamage handles damaging an item.
func (h *SessionHandler) HandleItemDamage(ctx *player.Context, it item.Stack, damage *int) {
	ev := eventItemDamagePool.Get().(*EventItemDamage)
	ev.Ctx = ctx
	ev.Item = it
	ev.Damage = damage
	h.session.Emit(ev)
	*ev = EventItemDamage{}
	eventItemDamagePool.Put(ev)
}

// HandleItemPickup handles picking up an item.
func (h *SessionHandler) HandleItemPickup(ctx *player.Context, it *item.Stack) {
	ev := eventItemPickupPool.Get().(*EventItemPickup)
	ev.Ctx = ctx
	ev.Item = it
	h.session.Emit(ev)
	*ev = EventItemPickup{}
	eventItemPickupPool.Put(ev)
}

// HandleHeldSlotChange handles held hotbar slot change.
func (h *SessionHandler) HandleHeldSlotChange(ctx *player.Context, from, to int) {
	ev := eventHeldSlotChangePool.Get().(*EventHeldSlotChange)
	ev.Ctx = ctx
	ev.From = from
	ev.To = to
	h.session.Emit(ev)
	*ev = EventHeldSlotChange{}
	eventHeldSlotChangePool.Put(ev)
}

// HandleItemDrop handles dropping an item.
func (h *SessionHandler) HandleItemDrop(ctx *player.Context, it item.Stack) {
	ev := eventItemDropPool.Get().(*EventItemDrop)
	ev.Ctx = ctx
	ev.Item = it
	h.session.Emit(ev)
	*ev = EventItemDrop{}
	eventItemDropPool.Put(ev)
}

// HandleTransfer handles server transfer.
func (h *SessionHandler) HandleTransfer(ctx *player.Context, addr *net.UDPAddr) {
	ev := eventTransferPool.Get().(*EventTransfer)
	ev.Ctx = ctx
	ev.Address = addr
	h.session.Emit(ev)
	*ev = EventTransfer{}
	eventTransferPool.Put(ev)
}

// HandleCommandExecution handles executing a command.
func (h *SessionHandler) HandleCommandExecution(ctx *player.Context, command cmd.Command, args []string) {
	ev := eventCommandExecutionPool.Get().(*EventCommandExecution)
	ev.Ctx = ctx
	ev.Command = command
	ev.Args = args
	h.session.Emit(ev)
	*ev = EventCommandExecution{}
	eventCommandExecutionPool.Put(ev)
}

// HandleQuit handles a player quitting the server.
func (h *SessionHandler) HandleQuit(p *player.Player) {
	ev := eventQuitPool.Get().(*EventQuit)
	ev.Player = p
	h.session.Emit(ev)
	*ev = EventQuit{}
	eventQuitPool.Put(ev)
	h.session.close()
}

// HandleDiagnostics handles a diagnostics request.
func (h *SessionHandler) HandleDiagnostics(p *player.Player, d session.Diagnostics) {
	ev := eventDiagnosticsPool.Get().(*EventDiagnostics)
	ev.Player = p
	ev.Diagnostics = d
	h.session.Emit(ev)
	*ev = EventDiagnostics{}
	eventDiagnosticsPool.Put(ev)
}

// registerHandler registers a handler type with the manager.
func (m *Manager) registerHandler(h any, bundle *Bundle) error {
	t := reflect.TypeOf(h)

	meta, err := analyzeSystem(t, bundle, m.registry)
	if err != nil {
		return err
	}

	// Set up pool to create correct type
	meta.Pool = &sync.Pool{
		New: func() any {
			return reflect.New(t.Elem()).Interface()
		},
	}

	// Scan for event methods and create optimized, unsafe emitters.
	events := make(map[reflect.Type]unsafeEventEmitter)
	for i := 0; i < t.NumMethod(); i++ {
		method := t.Method(i)
		if method.Type.NumIn() != 2 { // Receiver + 1 argument
			continue
		}

		eventType := method.Type.In(1)

		// For this unsafe optimization to work without assembly, we can only
		// support events that are passed as pointers. Handling value types
		// would require knowing the argument passing convention (registers vs.
		// stack), which is not guaranteed to be stable.
		if eventType.Kind() != reflect.Pointer {
			// We panic here to enforce the use of pointer types for events at startup.
			// This makes the performance characteristics clear to the developer.
			panic(fmt.Errorf("pecs: event handler %v method %v uses value type %v for event. only pointer types are supported for unsafe emission", t, method.Name, eventType))
		}

		// Get the method's code address
		capturedFptr := method.Func.Pointer()

		// Create a persistent funcval struct containing the code address.
		// This must be heap-allocated and persist for the lifetime of the emitter.
		fv := &funcval{fn: capturedFptr}

		// Reinterpret the *funcval as a callable function value.
		// In Go's runtime, a func variable IS a *funcval internally.
		fn := *(*func(unsafe.Pointer, unsafe.Pointer))(unsafe.Pointer(&fv))

		events[eventType] = func(handler, event unsafe.Pointer) {
			// Direct function call through the properly constructed function value.
			// This is ~1-2ns overhead, same as a regular function call.
			fn(handler, event)
		}
	}

	hm := &handlerMeta{
		meta:   meta,
		bundle: bundle,
		events: events,
	}

	// Separate global handlers from session-scoped handlers
	if meta.IsGlobal {
		m.globalHandlers = append(m.globalHandlers, hm)
	} else {
		m.handlers = append(m.handlers, hm)
	}

	return nil
}

// Copy unsafe.Pointer for use in handler module.
var _ unsafe.Pointer
