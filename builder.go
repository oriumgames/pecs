package pecs

import (
	"github.com/df-mc/dragonfly/server/world"
)

// Builder configures PECS before initialization.
// Use NewBuilder() to create a builder and chain configuration methods.
type Builder struct {
	bundles         []func(*Manager) *Bundle
	resources       []any
	peerProviders   []peerProviderRegistration
	sharedProviders []sharedProviderRegistration
}

type peerProviderRegistration struct {
	provider PeerProvider
	options  []ProviderOption
}

type sharedProviderRegistration struct {
	provider SharedProvider
	options  []ProviderOption
}

// NewBuilder creates a new PECS builder.
func NewBuilder() *Builder {
	return &Builder{}
}

// Bundle adds a bundle to the builder.
func (b *Builder) Bundle(callback func(*Manager) *Bundle) *Builder {
	b.bundles = append(b.bundles, callback)
	return b
}

// Resource adds a global resource available to all bundles.
func (b *Builder) Resource(res any) *Builder {
	b.resources = append(b.resources, res)
	return b
}

// PeerProvider registers a provider for Peer[T] resolution.
// PeerProviders fetch and sync data for remote players.
//
// Example:
//
//	builder.PeerProvider(&StatusProvider{...}, pecs.WithFetchTimeout(2000))
func (b *Builder) PeerProvider(p PeerProvider, opts ...ProviderOption) *Builder {
	b.peerProviders = append(b.peerProviders, peerProviderRegistration{p, opts})
	return b
}

// SharedProvider registers a provider for Shared[T] resolution.
// SharedProviders fetch and sync data for shared entities (parties, matches, etc.).
//
// Example:
//
//	builder.SharedProvider(&PartyProvider{...})
func (b *Builder) SharedProvider(p SharedProvider, opts ...ProviderOption) *Builder {
	b.sharedProviders = append(b.sharedProviders, sharedProviderRegistration{p, opts})
	return b
}

// Init initializes PECS with the configured settings.
// Returns the Manager instance which should be stored and used to create sessions.
// Multiple Manager instances can coexist for running multiple isolated servers.
func (b *Builder) Init(ws ...*world.World) *Manager {
	m := newManager(ws)

	var hooks []func(*Manager)

	// Add bundles
	for _, f := range b.bundles {
		bund := f(m)
		m.bundles = append(m.bundles, bund)
		hooks = append(hooks, bund.postInitHooks...)
	}

	// Add global resources
	for _, res := range b.resources {
		m.addResource(res)
	}

	for _, bundle := range m.bundles {
		for _, res := range bundle.resources {
			m.addResource(res)
		}
	}

	// Register federation providers
	for _, reg := range b.peerProviders {
		m.RegisterPeerProvider(reg.provider, reg.options...)
	}
	for _, reg := range b.sharedProviders {
		m.RegisterSharedProvider(reg.provider, reg.options...)
	}

	for _, bundle := range m.bundles {
		for _, reg := range bundle.peerProviders {
			m.RegisterPeerProvider(reg.provider, reg.options...)
		}
		for _, reg := range bundle.sharedProviders {
			m.RegisterSharedProvider(reg.provider, reg.options...)
		}
	}

	// Build all systems
	if err := m.build(); err != nil {
		panic("pecs: failed to build systems: " + err.Error())
	}

	// Start the scheduler
	m.Start()

	for _, hook := range hooks {
		hook(m)
	}

	return m
}
