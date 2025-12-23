package pecs

import (
	"context"
	"reflect"
)

// Provider is the base interface for all data providers.
// Providers bridge PECS with external data sources (gRPC services, databases, etc.).
type Provider interface {
	// Name returns a unique identifier for this provider (for logging/debugging).
	Name() string
}

// PeerProvider fetches and syncs per-player data for Peer[T] resolution.
// Implement this interface to enable cross-server player data access.
type PeerProvider interface {
	Provider

	// PlayerComponents returns the component types this provider handles.
	// PECS will only call this provider for Peer[T] where T is in this list.
	PlayerComponents() []reflect.Type

	// FetchPlayer retrieves components for a single player.
	// Returns nil (not error) if the player doesn't exist.
	FetchPlayer(ctx context.Context, playerID string) ([]any, error)

	// FetchPlayers batch-fetches components for multiple players.
	// Returns a map of playerID -> components.
	// Missing players should be omitted from the map (not nil values).
	FetchPlayers(ctx context.Context, playerIDs []string) (map[string][]any, error)

	// SubscribePlayer starts receiving real-time updates for a player.
	// Updates should be sent to the channel until the subscription is closed.
	// Return an error if the player doesn't exist or subscription fails.
	SubscribePlayer(ctx context.Context, playerID string, updates chan<- PlayerUpdate) (Subscription, error)
}

// SharedProvider fetches and syncs shared entity data for Shared[T] resolution.
// Implement this interface for data shared across multiple players (parties, matches, etc.).
type SharedProvider interface {
	Provider

	// EntityComponents returns the component types this provider handles.
	// PECS will only call this provider for Shared[T] where T is in this list.
	EntityComponents() []reflect.Type

	// FetchEntity retrieves a shared entity by ID.
	// Returns nil (not error) if the entity doesn't exist.
	FetchEntity(ctx context.Context, entityID string) (any, error)

	// FetchEntities batch-fetches components for multiple entities.
	// Returns a map of entityID -> component.
	// Missing entities should be omitted from the map.
	FetchEntities(ctx context.Context, entityIDs []string) (map[string]any, error)

	// SubscribeEntity starts receiving real-time updates for an entity.
	// Updates should be sent to the channel until the subscription is closed.
	// Return an error if the entity doesn't exist or subscription fails.
	SubscribeEntity(ctx context.Context, entityID string, updates chan<- any) (Subscription, error)
}

// PlayerUpdate represents an update to a player's component from a provider.
type PlayerUpdate struct {
	// ComponentType is the type of component being updated.
	ComponentType reflect.Type

	// Data is a pointer to the new component data.
	// If nil, the component should be removed.
	Data any
}

// Subscription represents an active subscription to updates.
// Call Close() to stop receiving updates and release resources.
type Subscription interface {
	Close() error
}

// ProviderOptions configures provider behavior.
type ProviderOptions struct {
	// FetchTimeout is the maximum time to wait for Fetch calls.
	// Default: 5 seconds.
	FetchTimeout int64

	// GracePeriod is how long to keep cached data after the last reference is released.
	// This prevents thrashing when players rapidly reference/dereference the same target.
	// Default: 30 seconds.
	GracePeriod int64

	// StaleTimeout defines when cached data is considered too old to use.
	// If a subscription fails and data is older than this, resolution fails.
	// Default: 5 minutes.
	StaleTimeout int64

	// Required indicates this provider must succeed for session creation.
	// If true, NewSession returns an error if this provider fails.
	// If false, provider failures are logged but session creation continues.
	// Default: false.
	Required bool
}

// defaultProviderOptions returns sensible defaults.
func defaultProviderOptions() ProviderOptions {
	return ProviderOptions{
		FetchTimeout: 5_000,      // 5 seconds
		GracePeriod:  30_000,     // 30 seconds
		StaleTimeout: 5 * 60_000, // 5 minutes
	}
}

// ProviderOption configures a provider.
type ProviderOption func(*ProviderOptions)

// WithFetchTimeout sets the fetch timeout in milliseconds.
func WithFetchTimeout(ms int64) ProviderOption {
	return func(o *ProviderOptions) {
		o.FetchTimeout = ms
	}
}

// WithGracePeriod sets the grace period in milliseconds.
func WithGracePeriod(ms int64) ProviderOption {
	return func(o *ProviderOptions) {
		o.GracePeriod = ms
	}
}

// WithStaleTimeout sets the stale timeout in milliseconds.
func WithStaleTimeout(ms int64) ProviderOption {
	return func(o *ProviderOptions) {
		o.StaleTimeout = ms
	}
}

// WithRequired marks the provider as required for session creation.
// If a required provider fails during NewSession, the session creation fails.
func WithRequired(required bool) ProviderOption {
	return func(o *ProviderOptions) {
		o.Required = required
	}
}
