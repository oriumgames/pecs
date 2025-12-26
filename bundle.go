package pecs

import (
	"reflect"
	"time"

	"github.com/df-mc/dragonfly/server/cmd"
)

// Bundle groups related systems, handlers, and resources together.
// Bundles are registered with the PECS builder and provide isolation
// between different gameplay features.
type Bundle struct {
	name string

	// handlers holds handler registrations
	handlers []handlerRegistration

	// loops holds loop system registrations
	loops []loopRegistration

	// tasks holds task system registrations (for metadata/pooling)
	tasks []taskRegistration

	// commands holds command registrations
	commands []commandRegistration

	// resources holds bundle-level resources (registered with global manager)
	resources []any

	postInitHooks []func(*Manager)

	// Federation providers
	peerProviders   []peerProviderRegistration
	sharedProviders []sharedProviderRegistration

	// meta holds computed metadata for systems
	handlerMeta []*SystemMeta
	loopMeta    []*SystemMeta
	taskMeta    map[reflect.Type]*SystemMeta
}

// handlerRegistration holds a handler registration.
type handlerRegistration struct {
	handler any
}

// loopRegistration holds a loop system registration.
type loopRegistration struct {
	system   Runnable
	interval time.Duration
	stage    Stage
}

// taskRegistration holds a task system registration.
type taskRegistration struct {
	system Runnable
	stage  Stage
}

// commandRegistration holds a command registration.
type commandRegistration struct {
	command cmd.Command
}

// NewBundle creates a new bundle with the given name.
func NewBundle(name string) *Bundle {
	return &Bundle{
		name:     name,
		taskMeta: make(map[reflect.Type]*SystemMeta),
	}
}

// Name returns the bundle name.
func (b *Bundle) Name() string {
	return b.name
}

// Resource registers a bundle-level resource.
// These are available to all systems via global manager.
func (b *Bundle) Resource(res any) *Bundle {
	b.resources = append(b.resources, res)
	return b
}

func (b *Bundle) PostInit(hook func(*Manager)) *Bundle {
	b.postInitHooks = append(b.postInitHooks, hook)
	return b
}

// Build returns a callback function that returns this bundle.
// This allows for cleaner inline bundle initialization:
//
//	bund := pecs.NewBundle("gameplay").
//	    Handler(&ExampleHandler{}).
//	    Build()
//
//	mngr := pecs.NewBuilder().
//	    Bundle(bund).
//	    Init()
func (b *Bundle) Build() func(*Manager) *Bundle {
	return func(*Manager) *Bundle {
		return b
	}
}

// Command registers a Dragonfly command for this bundle.
// Commands are automatically registered with Dragonfly's command system
// when the bundle is built.
func (b *Bundle) Command(command cmd.Command) *Bundle {
	b.commands = append(b.commands, commandRegistration{
		command: command,
	})
	return b
}

// Handler registers a handler for this bundle.
// Handlers are structs that implement event methods like HandleHurt(*EventHurt).
func (b *Bundle) Handler(h any) *Bundle {
	b.handlers = append(b.handlers, handlerRegistration{
		handler: h,
	})
	return b
}

// Loop registers a loop system that runs at fixed intervals.
// Interval of 0 means the loop runs every tick.
func (b *Bundle) Loop(sys Runnable, interval time.Duration, stage Stage) *Bundle {
	b.loops = append(b.loops, loopRegistration{
		system:   sys,
		interval: interval,
		stage:    stage,
	})
	return b
}

// Task registers a task system type for pooling optimization.
// Tasks are one-shot systems scheduled dynamically.
func (b *Bundle) Task(sys Runnable, stage Stage) *Bundle {
	b.tasks = append(b.tasks, taskRegistration{
		system: sys,
		stage:  stage,
	})
	return b
}

// PeerProvider registers a provider for Peer[T] resolution.
func (b *Bundle) PeerProvider(p PeerProvider, opts ...ProviderOption) *Bundle {
	b.peerProviders = append(b.peerProviders, peerProviderRegistration{p, opts})
	return b
}

// SharedProvider registers a provider for Shared[T] resolution.
func (b *Bundle) SharedProvider(p SharedProvider, opts ...ProviderOption) *Bundle {
	b.sharedProviders = append(b.sharedProviders, sharedProviderRegistration{p, opts})
	return b
}

// build analyzes all systems and computes metadata.
func (b *Bundle) build(registry *componentRegistry) error {
	// Build handler metadata
	for _, reg := range b.handlers {
		meta, err := analyzeSystem(reflect.TypeOf(reg.handler), b, registry)
		if err != nil {
			return err
		}
		b.handlerMeta = append(b.handlerMeta, meta)
	}

	// Build loop metadata
	for _, reg := range b.loops {
		meta, err := analyzeSystem(reflect.TypeOf(reg.system), b, registry)
		if err != nil {
			return err
		}
		meta.Stage = reg.stage
		b.loopMeta = append(b.loopMeta, meta)
	}

	// Build task metadata
	for _, reg := range b.tasks {
		t := reflect.TypeOf(reg.system)
		if t.Kind() == reflect.Ptr {
			t = t.Elem()
		}
		meta, err := analyzeSystem(t, b, registry)
		if err != nil {
			return err
		}
		meta.Stage = reg.stage
		b.taskMeta[t] = meta
	}

	// Register commands with Dragonfly's command system
	for _, reg := range b.commands {
		cmd.Register(reg.command)
	}

	return nil
}

// getTaskMeta retrieves task metadata by type.
func (b *Bundle) getTaskMeta(t reflect.Type) *SystemMeta {
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	return b.taskMeta[t]
}
