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
	// and the unsafe event dispatch in registerHandler needs updating.
	testAddr := uintptr(0xDEADBEEF)
	fv := &funcval{fn: testAddr}

	// Convert our funcval to a function value and back to verify round-trip
	fn := *(*func())(unsafe.Pointer(&fv))
	recovered := *(*uintptr)(unsafe.Pointer(&fn))

	if recovered != uintptr(unsafe.Pointer(fv)) {
		panic("pecs: funcval layout assumption violated - unsafe event dispatch will not work on this Go version")
	}
}

// emptyInterface is the header for an empty interface. It is used to
// extract the underlying data pointer from an interface value.
type emptyInterface struct {
	typ  unsafe.Pointer
	data unsafe.Pointer
}

// unsafeEventDispatcher is a function that calls an event handler method
// using unsafe pointers to avoid reflection overhead. The handler and event
// pointers are the raw data pointers from their respective interface values.
type unsafeEventDispatcher func(handler, event unsafe.Pointer)

// handlerMeta holds metadata and pool for a registered handler type.
type handlerMeta struct {
	meta   *SystemMeta
	bundle *Bundle
	// events is a map of event types to their unsafe, reflection-free dispatcher.
	events map[reflect.Type]unsafeEventDispatcher
}

// Handler is the primary interface for systems that react to player events.
// It extends the base dragonfly player.Handler with PECS-specific events like HandleJoin.
// Systems wishing to handle events should implement this interface, typically by
// embedding the NopHandler struct.
type Handler interface {
	player.Handler
	// HandleJoin is called once when a player's session is first created and
	// they are initialized in the world. It is the recommended place for all
	// initial join logic.
	HandleJoin(*player.Player)
}

// NopHandler provides a default, no-op implementation of the Handler interface.
// It is intended to be embedded in custom handler structs, allowing users to
// only implement the specific event methods they are interested in.
type NopHandler struct {
	player.NopHandler
}

// HandleJoin provides a default empty implementation for the NopHandler.
func (h *NopHandler) HandleJoin(p *player.Player) {}

// SessionHandler wraps session to implement Handler.
// It delegates events to all registered PECS handlers.
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

// NewHandler creates a new Handler for the given session.
func NewHandler(s *Session, p *player.Player) Handler {
	h := &SessionHandler{session: s}
	h.HandleJoin(p)
	return h
}

// Compile-time check that SessionHandler implements Handler.
var _ Handler = (*SessionHandler)(nil)

// executeHandlers runs all matching handlers for an event.
//
// Safety:
// This function relies on Dragonfly's event dispatching guarantees. It assumes
// it is running in a context where it is safe to access the player and world.
// Component injection uses internal locking to safely retrieve component pointers,
// ensuring no races with concurrent component addition/removal.
func (h *SessionHandler) executeHandlers(fn func(h Handler)) {
	s := h.session
	if s.manager == nil || s.closed.Load() {
		return
	}

	// Reusable single-element slice to avoid allocation per handler
	sessionSlice := [1]*Session{s}

	for _, hm := range s.manager.handlers {
		// Check bitmask
		if !s.canRun(hm.meta) {
			continue
		}

		// Get handler from pool
		handler := hm.meta.Pool.Get().(Handler)

		// Inject dependencies
		if !injectSystem(handler, sessionSlice[:], hm.meta, hm.bundle, s.manager) {
			zeroSystem(handler, hm.meta)
			hm.meta.Pool.Put(handler)
			continue
		}

		// Execute handler method
		fn(handler)

		// Zero and return to pool
		zeroSystem(handler, hm.meta)
		hm.meta.Pool.Put(handler)
	}
}

// Dispatch dispatches a custom event to all registered handlers that listen for it.
// Handlers listen for events by implementing a method with the signature:
//
//	func (h *MyHandler) HandleMyEvent(event MyEventType)
//
// The method name does not matter, only the signature (one argument).
func (s *Session) Dispatch(event any) {
	if s.manager == nil || s.closed.Load() {
		return
	}

	eventType := reflect.TypeOf(event)
	if eventType.Kind() != reflect.Pointer {
		// Log warning for non-pointer events - these can't be dispatched via unsafe path.
		// The registration path panics for handlers with value-type event parameters,
		// but this can still happen if Dispatch is called with a value directly.
		slog.Warn("pecs: dispatch ignored non-pointer event",
			"type", eventType.String(),
			"session", s.name)
		return
	}

	// Extract the raw data pointer from the event interface.
	eventPtr := (*emptyInterface)(unsafe.Pointer(&event)).data
	sessionSlice := [1]*Session{s}

	for _, hm := range s.manager.handlers {
		// Check if this handler handles this event type.
		dispatcher, ok := hm.events[eventType]
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

		// Execute the pre-compiled unsafe dispatcher.
		dispatcher(handlerPtr, eventPtr)

		// Zero and return to pool.
		zeroSystem(handler, hm.meta)
		hm.meta.Pool.Put(handler)
	}
}

// ComponentAttachEvent is dispatched when a component is added to a session.
type ComponentAttachEvent struct {
	ComponentType reflect.Type
}

// ComponentDetachEvent is dispatched when a component is removed from a session.
type ComponentDetachEvent struct {
	ComponentType reflect.Type
}

// HandleMove handles the player moving.
func (h *SessionHandler) HandleMove(ctx *player.Context, newPos mgl64.Vec3, newRot cube.Rotation) {
	h.executeHandlers(func(ph Handler) { ph.HandleMove(ctx, newPos, newRot) })
}

// HandleJump handles the player jumping.
func (h *SessionHandler) HandleJump(p *player.Player) {
	h.executeHandlers(func(ph Handler) { ph.HandleJump(p) })
}

// HandleTeleport handles the player being teleported.
func (h *SessionHandler) HandleTeleport(ctx *player.Context, pos mgl64.Vec3) {
	h.executeHandlers(func(ph Handler) { ph.HandleTeleport(ctx, pos) })
}

// HandleChangeWorld handles the player changing worlds.
func (h *SessionHandler) HandleChangeWorld(p *player.Player, before, after *world.World) {
	h.session.updateWorldCache(after)
	if h.session.manager != nil {
		h.session.manager.MoveSession(h.session, before, after)
	}
	h.executeHandlers(func(ph Handler) { ph.HandleChangeWorld(p, before, after) })
}

// HandleToggleSprint handles the player toggling sprint.
func (h *SessionHandler) HandleToggleSprint(ctx *player.Context, after bool) {
	h.executeHandlers(func(ph Handler) { ph.HandleToggleSprint(ctx, after) })
}

// HandleToggleSneak handles the player toggling sneak.
func (h *SessionHandler) HandleToggleSneak(ctx *player.Context, after bool) {
	h.executeHandlers(func(ph Handler) { ph.HandleToggleSneak(ctx, after) })
}

// HandleChat handles the player sending a chat message.
func (h *SessionHandler) HandleChat(ctx *player.Context, message *string) {
	h.executeHandlers(func(ph Handler) { ph.HandleChat(ctx, message) })
}

// HandleFoodLoss handles the player losing food.
func (h *SessionHandler) HandleFoodLoss(ctx *player.Context, from int, to *int) {
	h.executeHandlers(func(ph Handler) { ph.HandleFoodLoss(ctx, from, to) })
}

// HandleHeal handles the player being healed.
func (h *SessionHandler) HandleHeal(ctx *player.Context, health *float64, src world.HealingSource) {
	h.executeHandlers(func(ph Handler) { ph.HandleHeal(ctx, health, src) })
}

// HandleHurt handles the player being hurt.
func (h *SessionHandler) HandleHurt(ctx *player.Context, damage *float64, immune bool, attackImmunity *time.Duration, src world.DamageSource) {
	h.executeHandlers(func(ph Handler) { ph.HandleHurt(ctx, damage, immune, attackImmunity, src) })
}

// HandleDeath handles the player dying.
func (h *SessionHandler) HandleDeath(p *player.Player, src world.DamageSource, keepInv *bool) {
	h.executeHandlers(func(ph Handler) { ph.HandleDeath(p, src, keepInv) })
}

// HandleRespawn handles the player respawning.
func (h *SessionHandler) HandleRespawn(p *player.Player, pos *mgl64.Vec3, w **world.World) {
	h.executeHandlers(func(ph Handler) { ph.HandleRespawn(p, pos, w) })
}

// HandleSkinChange handles the player changing their skin.
func (h *SessionHandler) HandleSkinChange(ctx *player.Context, sk *skin.Skin) {
	h.executeHandlers(func(ph Handler) { ph.HandleSkinChange(ctx, sk) })
}

// HandleFireExtinguish handles the player extinguishing fire.
func (h *SessionHandler) HandleFireExtinguish(ctx *player.Context, pos cube.Pos) {
	h.executeHandlers(func(ph Handler) { ph.HandleFireExtinguish(ctx, pos) })
}

// HandleStartBreak handles the player starting to break a block.
func (h *SessionHandler) HandleStartBreak(ctx *player.Context, pos cube.Pos) {
	h.executeHandlers(func(ph Handler) { ph.HandleStartBreak(ctx, pos) })
}

// HandleBlockBreak handles block breaking.
func (h *SessionHandler) HandleBlockBreak(ctx *player.Context, pos cube.Pos, drops *[]item.Stack, xp *int) {
	h.executeHandlers(func(ph Handler) { ph.HandleBlockBreak(ctx, pos, drops, xp) })
}

// HandleBlockPlace handles block placement.
func (h *SessionHandler) HandleBlockPlace(ctx *player.Context, pos cube.Pos, b world.Block) {
	h.executeHandlers(func(ph Handler) { ph.HandleBlockPlace(ctx, pos, b) })
}

// HandleBlockPick handles picking a block.
func (h *SessionHandler) HandleBlockPick(ctx *player.Context, pos cube.Pos, b world.Block) {
	h.executeHandlers(func(ph Handler) { ph.HandleBlockPick(ctx, pos, b) })
}

// HandleItemUse handles general item use.
func (h *SessionHandler) HandleItemUse(ctx *player.Context) {
	h.executeHandlers(func(ph Handler) { ph.HandleItemUse(ctx) })
}

// HandleItemUseOnBlock handles using an item on a block.
func (h *SessionHandler) HandleItemUseOnBlock(ctx *player.Context, pos cube.Pos, face cube.Face, clickPos mgl64.Vec3) {
	h.executeHandlers(func(ph Handler) { ph.HandleItemUseOnBlock(ctx, pos, face, clickPos) })
}

// HandleItemUseOnEntity handles using an item on an entity.
func (h *SessionHandler) HandleItemUseOnEntity(ctx *player.Context, e world.Entity) {
	h.executeHandlers(func(ph Handler) { ph.HandleItemUseOnEntity(ctx, e) })
}

// HandleItemRelease handles releasing a charged-use item.
func (h *SessionHandler) HandleItemRelease(ctx *player.Context, it item.Stack, dur time.Duration) {
	h.executeHandlers(func(ph Handler) { ph.HandleItemRelease(ctx, it, dur) })
}

// HandleItemConsume handles consuming an item.
func (h *SessionHandler) HandleItemConsume(ctx *player.Context, it item.Stack) {
	h.executeHandlers(func(ph Handler) { ph.HandleItemConsume(ctx, it) })
}

// HandleAttackEntity handles attacking an entity.
func (h *SessionHandler) HandleAttackEntity(ctx *player.Context, e world.Entity, force, height *float64, critical *bool) {
	h.executeHandlers(func(ph Handler) { ph.HandleAttackEntity(ctx, e, force, height, critical) })
}

// HandleExperienceGain handles XP gain.
func (h *SessionHandler) HandleExperienceGain(ctx *player.Context, amount *int) {
	h.executeHandlers(func(ph Handler) { ph.HandleExperienceGain(ctx, amount) })
}

// HandlePunchAir handles punching air.
func (h *SessionHandler) HandlePunchAir(ctx *player.Context) {
	h.executeHandlers(func(ph Handler) { ph.HandlePunchAir(ctx) })
}

// HandleSignEdit handles sign text editing.
func (h *SessionHandler) HandleSignEdit(ctx *player.Context, pos cube.Pos, frontSide bool, oldText, newText string) {
	h.executeHandlers(func(ph Handler) { ph.HandleSignEdit(ctx, pos, frontSide, oldText, newText) })
}

// HandleLecternPageTurn handles page turning on lecterns.
func (h *SessionHandler) HandleLecternPageTurn(ctx *player.Context, pos cube.Pos, oldPage int, newPage *int) {
	h.executeHandlers(func(ph Handler) { ph.HandleLecternPageTurn(ctx, pos, oldPage, newPage) })
}

// HandleItemDamage handles damaging an item.
func (h *SessionHandler) HandleItemDamage(ctx *player.Context, it item.Stack, damage int) {
	h.executeHandlers(func(ph Handler) { ph.HandleItemDamage(ctx, it, damage) })
}

// HandleItemPickup handles picking up an item.
func (h *SessionHandler) HandleItemPickup(ctx *player.Context, it *item.Stack) {
	h.executeHandlers(func(ph Handler) { ph.HandleItemPickup(ctx, it) })
}

// HandleHeldSlotChange handles held hotbar slot change.
func (h *SessionHandler) HandleHeldSlotChange(ctx *player.Context, from, to int) {
	h.executeHandlers(func(ph Handler) { ph.HandleHeldSlotChange(ctx, from, to) })
}

// HandleItemDrop handles dropping an item.
func (h *SessionHandler) HandleItemDrop(ctx *player.Context, it item.Stack) {
	h.executeHandlers(func(ph Handler) { ph.HandleItemDrop(ctx, it) })
}

// HandleTransfer handles server transfer.
func (h *SessionHandler) HandleTransfer(ctx *player.Context, addr *net.UDPAddr) {
	h.executeHandlers(func(ph Handler) { ph.HandleTransfer(ctx, addr) })
}

// HandleCommandExecution handles executing a command.
func (h *SessionHandler) HandleCommandExecution(ctx *player.Context, command cmd.Command, args []string) {
	h.executeHandlers(func(ph Handler) { ph.HandleCommandExecution(ctx, command, args) })
}

// HandleJoin handles a player joining the server.
func (h *SessionHandler) HandleJoin(p *player.Player) {
	h.executeHandlers(func(ph Handler) { ph.HandleJoin(p) })
}

// HandleQuit handles a player quitting the server.
func (h *SessionHandler) HandleQuit(p *player.Player) {
	h.executeHandlers(func(ph Handler) { ph.HandleQuit(p) })
	defer h.session.close()
}

// HandleDiagnostics handles a diagnostics request.
func (h *SessionHandler) HandleDiagnostics(p *player.Player, d session.Diagnostics) {
	h.executeHandlers(func(ph Handler) { ph.HandleDiagnostics(p, d) })
}

// registerHandler registers a handler type with the manager.
func (m *Manager) registerHandler(h Handler, bundle *Bundle) error {
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

	// Scan for event methods and create optimized, unsafe dispatchers.
	events := make(map[reflect.Type]unsafeEventDispatcher)
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
			panic(fmt.Errorf("pecs: event handler %v method %v uses value type %v for event. only pointer types are supported for unsafe dispatch", t, method.Name, eventType))
		}

		// Get the method's code address
		capturedFptr := method.Func.Pointer()

		// Create a persistent funcval struct containing the code address.
		// This must be heap-allocated and persist for the lifetime of the dispatcher.
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

	m.handlers = append(m.handlers, &handlerMeta{
		meta:   meta,
		bundle: bundle,
		events: events,
	})

	return nil
}

// Copy unsafe.Pointer for use in handler module.
var _ unsafe.Pointer
