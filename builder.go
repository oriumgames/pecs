package pecs

import (
	"time"

	"github.com/df-mc/dragonfly/server/cmd"
)

// Builder configures PECS before initialization.
// Use NewBuilder() to create a builder and chain configuration methods.
type Builder struct {
	bundles    []*Bundle
	injections []any
}

// NewBuilder creates a new PECS builder.
func NewBuilder() *Builder {
	return &Builder{}
}

// Bundle adds a bundle to the builder.
func (b *Builder) Bundle(bundle *Bundle) *Builder {
	b.bundles = append(b.bundles, bundle)
	return b
}

// Injection adds a global injection available to all bundles.
func (b *Builder) Injection(inj any) *Builder {
	b.injections = append(b.injections, inj)
	return b
}

// Resource adds a resource to an implicit default bundle.
func (b *Builder) Resource(res any) *Builder {
	bundle := b.getOrCreateDefaultBundle()
	bundle.Resource(res)
	return b
}

// Command adds a command to an implicit default bundle.
func (b *Builder) Command(command cmd.Command) *Builder {
	bundle := b.getOrCreateDefaultBundle()
	bundle.Command(command)
	return b
}

// Handler adds a handler to an implicit default bundle.
func (b *Builder) Handler(h Handler) *Builder {
	bundle := b.getOrCreateDefaultBundle()
	bundle.Handler(h)
	return b
}

// Loop adds a loop system to an implicit default bundle.
func (b *Builder) Loop(sys Runnable, interval time.Duration, stage Stage) *Builder {
	bundle := b.getOrCreateDefaultBundle()
	bundle.Loop(sys, interval, stage)
	return b
}

// Task adds a task type to an implicit default bundle.
func (b *Builder) Task(sys Runnable, stage Stage) *Builder {
	bundle := b.getOrCreateDefaultBundle()
	bundle.Task(sys, stage)
	return b
}

// getOrCreateDefaultBundle returns the default bundle, creating it if needed.
func (b *Builder) getOrCreateDefaultBundle() *Bundle {
	for _, bundle := range b.bundles {
		if bundle.name == "default" {
			return bundle
		}
	}
	bundle := NewBundle("default")
	b.bundles = append(b.bundles, bundle)
	return bundle
}

// Init initializes PECS with the configured settings.
// Returns the Manager instance which should be stored and used to create sessions.
// Multiple Manager instances can coexist for running multiple isolated servers.
func (b *Builder) Init() *Manager {
	m := newManager()

	// Add bundles
	m.bundles = b.bundles

	// Add global injections
	for _, inj := range b.injections {
		m.addInjection(inj)
	}

	// Build all systems
	if err := m.build(); err != nil {
		panic("pecs: failed to build systems: " + err.Error())
	}

	// Start the scheduler
	m.Start()

	return m
}

// MustInit is like Init but panics on error.
// This is a convenience method for simple setups.
func (b *Builder) MustInit() *Manager {
	return b.Init()
}
