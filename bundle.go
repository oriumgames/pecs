package pecs

import (
	"reflect"
	"sync"
	"time"
	"unsafe"

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

	// resources holds bundle-scoped resources
	resources   map[reflect.Type]unsafe.Pointer
	resourcesMu sync.RWMutex

	// injections holds bundle-level injections (in addition to global)
	injections   map[reflect.Type]unsafe.Pointer
	injectionsMu sync.RWMutex

	// meta holds computed metadata for systems
	handlerMeta []*SystemMeta
	loopMeta    []*SystemMeta
	taskMeta    map[reflect.Type]*SystemMeta
}

// handlerRegistration holds a handler registration.
type handlerRegistration struct {
	handler Handler
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
		name:       name,
		resources:  make(map[reflect.Type]unsafe.Pointer),
		injections: make(map[reflect.Type]unsafe.Pointer),
		taskMeta:   make(map[reflect.Type]*SystemMeta),
	}
}

// Name returns the bundle name.
func (b *Bundle) Name() string {
	return b.name
}

// Injection registers a bundle-level injection.
// These are available to all systems in this bundle.
func (b *Bundle) Injection(inj any) *Bundle {
	t := reflect.TypeOf(inj)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	b.injectionsMu.Lock()
	b.injections[t] = unsafe.Pointer(reflect.ValueOf(inj).Pointer())
	b.injectionsMu.Unlock()

	return b
}

// Resource registers a bundle-scoped resource.
// Resources are shared across all systems in this bundle.
func (b *Bundle) Resource(res any) *Bundle {
	t := reflect.TypeOf(res)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	b.resourcesMu.Lock()
	b.resources[t] = unsafe.Pointer(reflect.ValueOf(res).Pointer())
	b.resourcesMu.Unlock()

	return b
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
// Handlers implement Handler and respond to Dragonfly events.
func (b *Bundle) Handler(h Handler) *Bundle {
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

// getResource retrieves a resource by type.
func (b *Bundle) getResource(t reflect.Type) unsafe.Pointer {
	b.resourcesMu.RLock()
	defer b.resourcesMu.RUnlock()
	return b.resources[t]
}

// getInjection retrieves a bundle injection by type.
func (b *Bundle) getInjection(t reflect.Type) unsafe.Pointer {
	b.injectionsMu.RLock()
	defer b.injectionsMu.RUnlock()
	return b.injections[t]
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
