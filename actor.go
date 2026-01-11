package pecs

import (
	"github.com/df-mc/dragonfly/server/block/cube"
	"github.com/df-mc/dragonfly/server/entity/effect"
	"github.com/df-mc/dragonfly/server/item/inventory"
	"github.com/df-mc/dragonfly/server/player/skin"
	"github.com/df-mc/dragonfly/server/world"
	"github.com/go-gl/mathgl/mgl64"
)

// ActorConfig configures the initial state of a fake player or NPC entity.
// It is used with Manager.SpawnFake and Manager.SpawnEntity.
type ActorConfig struct {
	// Identity & Core Settings
	Name     string
	Skin     skin.Skin
	GameMode world.GameMode

	// Position & Physics
	Position     mgl64.Vec3
	Velocity     mgl64.Vec3
	Rotation     cube.Rotation
	FallDistance float64

	// Vitals: Health
	Health    float64
	HealthMax float64

	// Vitals: Hunger
	Food       int
	FoodTick   int
	Saturation float64
	Exhaustion float64

	// Vitals: Breath
	AirSupply    int
	AirSupplyMax int

	// Inventory & Equipment
	Inventory  *inventory.Inventory
	EnderChest *inventory.Inventory
	OffHand    *inventory.Inventory
	Armour     *inventory.Armour
	HeldSlot   int

	// State, Effects & Progression
	Experience      int
	EnchantmentSeed int64
	FireTicks       int64
	Effects         []effect.Effect
}

// FakeMarker marks a session as a fake player (testing bot).
// The federated ID is stored on Session.fakeID, not in the marker.
type FakeMarker struct{}

// EntityMarker marks a session as an NPC entity.
// Entities do not participate in cross-server provider lookups.
type EntityMarker struct{}
