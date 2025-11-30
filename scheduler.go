package pecs

import (
	"fmt"
	"os"
	"runtime"
	"runtime/debug"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/df-mc/dragonfly/server/player"
	"github.com/df-mc/dragonfly/server/world"
)

// Scheduler manages the execution of loops and tasks.
// It supports parallel execution of non-conflicting systems.
type Scheduler struct {
	manager *Manager

	// Loop management
	loops   [stageCount][]*loopState
	batches [stageCount][][]*loopState
	loopsMu sync.RWMutex

	// Worker pool
	workers    int
	workerPool chan func()
	workerWG   sync.WaitGroup

	// Execution state
	running      atomic.Bool
	stopCh       chan struct{}
	doneCh       chan struct{}
	shutdownOnce sync.Once

	// Tick tracking
	tickRate   time.Duration
	lastTick   time.Time
	tickNumber uint64
}

// loopState tracks the state of a single loop system.
type loopState struct {
	meta     *SystemMeta
	bundle   *Bundle
	interval time.Duration
	lastRun  time.Time
	nextRun  time.Time
}

// ShouldRun checks if the loop should run at the given time.
func (l *loopState) ShouldRun(now time.Time) bool {
	if l.interval == 0 {
		return true
	}
	return !now.Before(l.nextRun)
}

// MarkRun updates the last run time and schedules the next run.
func (l *loopState) MarkRun(now time.Time) {
	l.lastRun = now
	if l.interval > 0 {
		// Drift-free timing
		l.nextRun = l.nextRun.Add(l.interval)
		if l.nextRun.Before(now) {
			// Catch up if we're behind
			l.nextRun = now.Add(l.interval)
		}
	}
}

// newScheduler creates a new scheduler.
func newScheduler(manager *Manager) *Scheduler {
	workers := runtime.GOMAXPROCS(0)
	if workers < 1 {
		workers = 1
	}

	return &Scheduler{
		manager:    manager,
		workers:    workers,
		workerPool: make(chan func(), workers*4),
		tickRate:   50 * time.Millisecond, // 20 TPS
		stopCh:     make(chan struct{}),
		doneCh:     make(chan struct{}),
	}
}

// Start begins the scheduler's tick loop.
func (s *Scheduler) Start() {
	if s.running.Swap(true) {
		return // Already running
	}

	// Start worker pool
	for i := 0; i < s.workers; i++ {
		s.workerWG.Add(1)
		go s.worker()
	}

	// Start tick loop
	go s.tickLoop()
}

// Stop gracefully shuts down the scheduler.
func (s *Scheduler) Stop() {
	if !s.running.Swap(false) {
		return // Not running
	}

	close(s.stopCh)
	<-s.doneCh

	close(s.workerPool)
	s.workerWG.Wait()
}

// worker is a pool worker that executes jobs.
func (s *Scheduler) worker() {
	defer s.workerWG.Done()
	for fn := range s.workerPool {
		fn()
	}
}

// tickLoop is the main scheduler loop.
func (s *Scheduler) tickLoop() {
	defer close(s.doneCh)

	ticker := time.NewTicker(s.tickRate)
	defer ticker.Stop()

	for {
		select {
		case <-s.stopCh:
			return

		case now := <-ticker.C:
			s.tick(now)

		case <-s.manager.taskQueue.Notify():
			// Process immediate tasks
			s.processTasks(time.Now())
		}
	}
}

// tick executes one scheduler tick.
func (s *Scheduler) tick(now time.Time) {
	s.tickNumber++
	s.lastTick = now

	// Group sessions by world for transaction batching
	worldSessions := s.manager.groupedSessions()

	// Execute loops by stage
	for stage := Before; stage < stageCount; stage++ {
		s.runLoopsForStage(now, stage, worldSessions)
	}

	// Process due tasks
	s.processTasks(now)
}

// runLoopsForStage executes all loops for a given stage in parallel batches.
func (s *Scheduler) runLoopsForStage(now time.Time, stage Stage, worldSessions map[*world.World][]*Session) {
	s.loopsMu.RLock()
	batches := s.batches[stage]
	s.loopsMu.RUnlock()

	if len(batches) == 0 {
		return
	}

	var wg sync.WaitGroup

	for w, sessions := range worldSessions {
		if w == nil || len(sessions) == 0 {
			continue
		}

		wg.Add(1)
		w := w
		sessions := sessions

		// Submit world processing to worker pool
		job := func() {
			defer wg.Done()
			s.processWorldBatches(now, w, sessions, batches)
		}

		select {
		case s.workerPool <- job:
		default:
			// Worker pool full, run inline
			job()
		}
	}

	wg.Wait()
}

// processWorldBatches runs the loop batches for a specific world.
func (s *Scheduler) processWorldBatches(now time.Time, w *world.World, sessions []*Session, batches [][]*loopState) {
	for _, batch := range batches {
		// Filter loops that are due
		var runnableLoops []*loopState
		for _, loop := range batch {
			if loop.ShouldRun(now) {
				runnableLoops = append(runnableLoops, loop)
			}
		}

		if len(runnableLoops) == 0 {
			continue
		}

		// Execute batch in its own transaction
		w.Exec(func(tx *world.Tx) {
			// Filter valid sessions for this transaction
			validSessions := make([]*Session, 0, len(sessions))
			for _, sess := range sessions {
				if sess.closed.Load() {
					continue
				}
				p := s.manager.getPlayerFromTx(tx, sess)
				if p == nil {
					continue
				}
				validSessions = append(validSessions, sess)
			}

			if len(validSessions) == 0 {
				return
			}

			// Run loops in parallel
			var batchWG sync.WaitGroup
			batchWG.Add(len(runnableLoops))

			for _, loop := range runnableLoops {
				loop := loop
				go func() {
					defer batchWG.Done()
					s.executeLoopForSessions(validSessions, loop)
				}()
			}

			batchWG.Wait()
		})

		// Update timing
		for _, loop := range runnableLoops {
			loop.MarkRun(now)
		}
	}
}

// executeLoopForSessions runs a single loop system for all provided sessions.
func (s *Scheduler) executeLoopForSessions(sessions []*Session, loop *loopState) {
	system := loop.meta.Pool.Get().(Runnable)
	defer func() {
		zeroSystem(system, loop.meta)
		loop.meta.Pool.Put(system)
	}()

	for _, sess := range sessions {
		// Check bitmask
		if !sess.canRun(loop.meta) {
			continue
		}

		// Inject dependencies
		if !injectSystem(system, []*Session{sess}, loop.meta, loop.bundle, s.manager) {
			// Zero before next iteration to prevent stale data
			zeroSystem(system, loop.meta)
			continue
		}

		// Execute with panic recovery
		func() {
			defer func() {
				if r := recover(); r != nil {
					s.handleSystemPanic("loop", loop.meta.Name, r)
				}
			}()
			system.Run()
		}()

		// Zero after each execution for safety
		zeroSystem(system, loop.meta)
	}
}

// addLoop registers a loop with the scheduler.
func (s *Scheduler) addLoop(meta *SystemMeta, bundle *Bundle, interval time.Duration, stage Stage) {
	s.loopsMu.Lock()
	defer s.loopsMu.Unlock()

	state := &loopState{
		meta:     meta,
		bundle:   bundle,
		interval: interval,
		nextRun:  time.Now(),
	}

	s.loops[stage] = append(s.loops[stage], state)
	s.rebuildBatches(stage)
}

// rebuildBatches recomputes the execution batches for a stage based on conflicts.
func (s *Scheduler) rebuildBatches(stage Stage) {
	loops := s.loops[stage]
	if len(loops) == 0 {
		s.batches[stage] = nil
		return
	}

	// Sort loops by name to ensure deterministic batching
	sort.Slice(loops, func(i, j int) bool {
		return loops[i].meta.Name < loops[j].meta.Name
	})

	var batches [][]*loopState

	// Working set of loops to place
	remaining := make([]*loopState, len(loops))
	copy(remaining, loops)

	for len(remaining) > 0 {
		var batch []*loopState
		var nextRemaining []*loopState

		for _, candidate := range remaining {
			conflict := false
			for _, existing := range batch {
				if candidate.meta.Access.Conflicts(&existing.meta.Access) {
					conflict = true
					break
				}
			}

			if !conflict {
				batch = append(batch, candidate)
			} else {
				nextRemaining = append(nextRemaining, candidate)
			}
		}

		batches = append(batches, batch)
		remaining = nextRemaining
	}

	s.batches[stage] = batches
}

// processTasks processes all due tasks.
func (s *Scheduler) processTasks(now time.Time) {
	dueTasks := s.manager.taskQueue.PopDue(now)
	if len(dueTasks) == 0 {
		return
	}

	// Group tasks by stage
	tasksByStage := make([][]*scheduledTask, stageCount)
	for _, task := range dueTasks {
		stage := task.meta.Stage
		tasksByStage[stage] = append(tasksByStage[stage], task)
	}

	// Execute by stage
	for stage := Before; stage < stageCount; stage++ {
		s.executeTasksForStage(tasksByStage[stage])
	}
}

// executeTasksForStage executes all tasks for a stage.
func (s *Scheduler) executeTasksForStage(tasks []*scheduledTask) {
	if len(tasks) == 0 {
		return
	}

	// Group by world
	worldTasks := make(map[*world.World][]*scheduledTask)
	for _, task := range tasks {
		if task.cancelled.Load() {
			continue
		}

		// Validate all sessions
		allValid := true
		var taskWorld *world.World
		for _, sess := range task.sessions {
			if sess.closed.Load() {
				allValid = false
				break
			}
			// Get world from first session
			if taskWorld == nil {
				sess.Exec(func(tx *world.Tx, p *player.Player) {
					taskWorld = tx.World()
				})
			}
		}

		if !allValid || taskWorld == nil {
			continue
		}

		worldTasks[taskWorld] = append(worldTasks[taskWorld], task)
	}

	// Execute per world
	var wg sync.WaitGroup
	for w, wTasks := range worldTasks {
		wg.Add(1)
		w := w
		wTasks := wTasks

		job := func() {
			defer wg.Done()
			s.executeTasksForWorld(w, wTasks)
		}

		select {
		case s.workerPool <- job:
		default:
			job()
			wg.Done()
		}
	}
	wg.Wait()
}

// executeTasksForWorld executes tasks within a world transaction.
func (s *Scheduler) executeTasksForWorld(w *world.World, tasks []*scheduledTask) {
	w.Exec(func(tx *world.Tx) {
		for _, task := range tasks {
			if task.cancelled.Load() {
				continue
			}
			s.executeTask(tx, task)
		}
	})
}

func (s *Scheduler) handleSystemPanic(kind, name string, recovered any) {
	err := fmt.Errorf("pecs: panic in %s %s: %v\n%s", kind, name, recovered, debug.Stack())
	s.shutdownOnce.Do(func() {
		go func(k, n string, e error) {
			s.manager.Shutdown()
			fmt.Println(e.Error())
			os.Exit(1)
		}(kind, name, err)
	})
}

// executeTask executes a single task.
func (s *Scheduler) executeTask(tx *world.Tx, task *scheduledTask) {
	// Validate sessions are in this transaction
	for _, sess := range task.sessions {
		p := s.manager.getPlayerFromTx(tx, sess)
		if p == nil {
			return // Session not in this world
		}
	}

	// Check bitmasks for all sessions/windows
	if task.meta.IsMultiSession {
		for i, sess := range task.sessions {
			if i < len(task.meta.Windows) {
				window := &task.meta.Windows[i]
				sess.mu.RLock()
				if !sess.mask.ContainsAll(window.RequireMask) || sess.mask.ContainsAny(window.ExcludeMask) {
					sess.mu.RUnlock()
					return
				}
				sess.mu.RUnlock()
			}
		}
	} else if len(task.sessions) > 0 {
		if !task.sessions[0].canRun(task.meta) {
			return
		}
	}

	// Inject dependencies
	if !injectSystem(task.task, task.sessions, task.meta, task.bundle, s.manager) {
		return
	}

	// Execute
	func() {
		defer func() {
			if r := recover(); r != nil {
				s.handleSystemPanic("task", task.meta.Name, r)
			}
		}()
		task.task.Run()
	}()

	// Remove from session pending lists
	for _, sess := range task.sessions {
		sess.removeTask(task)
	}
}
