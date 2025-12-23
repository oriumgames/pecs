package pecs

import (
	"github.com/df-mc/dragonfly/server/player/skin"
	"github.com/go-gl/mathgl/mgl64"
)

// ActorConfig contains settings for spawning a fake player or NPC entity.
type ActorConfig struct {
	// Name is the display name of the actor.
	Name string
	// Skin is the skin applied to the actor.
	Skin skin.Skin
	// Position is the spawn position in the world.
	Position mgl64.Vec3
	// Yaw is the horizontal rotation.
	Yaw float64
	// Pitch is the vertical rotation.
	Pitch float64
}

// FakeMarker marks a session as a fake player (testing bot).
// The federated ID is stored on Session.fakeID, not in the marker.
type FakeMarker struct{}

// EntityMarker marks a session as an NPC entity.
// Entities do not participate in cross-server provider lookups.
type EntityMarker struct{}
