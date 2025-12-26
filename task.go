package pecs

import (
	"reflect"
	"sync"
	"sync/atomic"
	"time"

	"github.com/df-mc/dragonfly/server/world"
)

// scheduledTask represents a task scheduled for future execution.
type scheduledTask struct {
	// executeAt is the time the task should execute
	executeAt time.Time

	// sessions are the sessions involved in this task
	sessions []*Session

	// task is the task instance with payload
	task Runnable

	// meta is the pre-computed metadata for this task type
	meta *SystemMeta

	// bundle is the bundle this task belongs to (for resource access)
	bundle *Bundle

	// cancelled indicates if the task has been cancelled
	cancelled atomic.Bool

	// index is the heap index for efficient removal
	index int
}

// taskQueue is a priority queue for scheduled tasks.
// It uses a binary heap for O(log n) insertion and removal.
type taskQueue struct {
	mu    sync.Mutex
	heap  []*scheduledTask
	notif chan struct{}
}

// newTaskQueue creates a new task queue.
func newTaskQueue() *taskQueue {
	return &taskQueue{
		heap:  make([]*scheduledTask, 0, 64),
		notif: make(chan struct{}, 1),
	}
}

// compactHeap removes cancelled tasks from the heap and rebuilds the heap property.
func (q *taskQueue) compactHeap() {
	write := 0
	for read := 0; read < len(q.heap); read++ {
		if !q.heap[read].cancelled.Load() {
			q.heap[write] = q.heap[read]
			q.heap[write].index = write
			write++
		}
	}

	for i := write; i < len(q.heap); i++ {
		q.heap[i] = nil
	}
	q.heap = q.heap[:write]

	for i := len(q.heap)/2 - 1; i >= 0; i-- {
		q.down(i, len(q.heap))
	}
}

// Push adds a task to the queue with periodic cleanup to prevent memory leaks.
func (q *taskQueue) Push(task *scheduledTask) {
	q.mu.Lock()

	if len(q.heap) > 100 && len(q.heap)%100 == 0 {
		q.compactHeap()
	}

	q.push(task)
	q.mu.Unlock()

	select {
	case q.notif <- struct{}{}:
	default:
	}
}

// push adds a task without locking. Caller must hold lock.
func (q *taskQueue) push(task *scheduledTask) {
	task.index = len(q.heap)
	q.heap = append(q.heap, task)
	q.up(task.index)
}

// PopDue removes and returns all tasks that are due (executeAt <= now).
// Also tracks cancelled tasks encountered and triggers compaction if many are found.
func (q *taskQueue) PopDue(now time.Time) []*scheduledTask {
	q.mu.Lock()
	defer q.mu.Unlock()

	var due []*scheduledTask
	cancelledCount := 0

	for len(q.heap) > 0 && !q.heap[0].executeAt.After(now) {
		task := q.pop()
		if !task.cancelled.Load() {
			due = append(due, task)
		} else {
			cancelledCount++
		}
	}

	if cancelledCount > 50 && len(q.heap) > 0 {
		q.compactHeap()
	}

	return due
}

// Peek returns the next due time without removing.
func (q *taskQueue) Peek() (time.Time, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()

	if len(q.heap) == 0 {
		return time.Time{}, false
	}
	return q.heap[0].executeAt, true
}

// Remove cancels and removes a task from the queue.
func (q *taskQueue) Remove(task *scheduledTask) {
	task.cancelled.Store(true)
}

// Len returns the number of tasks in the queue.
func (q *taskQueue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.heap)
}

// Clear removes all tasks from the queue.
func (q *taskQueue) Clear() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.heap = q.heap[:0]
}

// Notify returns the notification channel.
func (q *taskQueue) Notify() <-chan struct{} {
	return q.notif
}

// pop removes and returns the minimum task. Caller must hold lock.
func (q *taskQueue) pop() *scheduledTask {
	n := len(q.heap) - 1
	q.swap(0, n)
	q.down(0, n)
	task := q.heap[n]
	q.heap[n] = nil // Allow GC
	q.heap = q.heap[:n]
	task.index = -1
	return task
}

// up moves task at index up the heap.
func (q *taskQueue) up(i int) {
	for {
		parent := (i - 1) / 2
		if parent == i || !q.heap[i].executeAt.Before(q.heap[parent].executeAt) {
			break
		}
		q.swap(i, parent)
		i = parent
	}
}

// down moves task at index down the heap.
func (q *taskQueue) down(i, n int) {
	for {
		left := 2*i + 1
		if left >= n || left < 0 {
			break
		}
		j := left
		if right := left + 1; right < n && q.heap[right].executeAt.Before(q.heap[left].executeAt) {
			j = right
		}
		if !q.heap[j].executeAt.Before(q.heap[i].executeAt) {
			break
		}
		q.swap(i, j)
		i = j
	}
}

// swap swaps two tasks in the heap.
func (q *taskQueue) swap(i, j int) {
	q.heap[i], q.heap[j] = q.heap[j], q.heap[i]
	q.heap[i].index = i
	q.heap[j].index = j
}

// TaskHandle allows cancelling a scheduled task.
type TaskHandle struct {
	task *scheduledTask
}

// Cancel cancels the scheduled task.
func (h *TaskHandle) Cancel() {
	if h != nil && h.task != nil {
		h.task.cancelled.Store(true)
	}
}

// ScheduleGlobal schedules a global task for execution after a given delay.
// The task runs once in the manager's default world and is not tied to any session.
// Returns a TaskHandle that can be used to cancel the task.
func ScheduleGlobal(m *Manager, task Runnable, delay time.Duration) *TaskHandle {
	if m == nil {
		return nil
	}

	taskType := reflect.TypeOf(task)
	meta := m.getTaskMeta(taskType)
	if meta == nil {
		// Task type not registered - analyze on the fly. This is less optimal
		// but allows for scheduling unregistered task types.
		var err error
		meta, err = analyzeSystem(taskType, nil, m.registry)
		if err != nil {
			return nil
		}
	}

	var bundle *Bundle
	for _, b := range m.bundles {
		if b.getTaskMeta(taskType) != nil {
			bundle = b
			break
		}
	}

	scheduled := &scheduledTask{
		executeAt: time.Now().Add(delay),
		sessions:  nil, // An empty session slice indicates a global task.
		task:      task,
		meta:      meta,
		bundle:    bundle,
	}

	m.taskQueue.Push(scheduled)

	return &TaskHandle{task: scheduled}
}

// DispatchGlobal schedules a global task for execution immediately.
// The task runs once in the manager's default world and is not tied to any session.
// Returns a TaskHandle that can be used to cancel the task.
func DispatchGlobal(s *Session, task Runnable) *TaskHandle {
	return ScheduleGlobal(s.manager, task, 0)
}

// Schedule schedules a task for execution after the given delay.
// The task will only run if the session passes the bitmask check at execution time.
// Returns a TaskHandle that can be used to cancel the task.
func Schedule(s *Session, task Runnable, delay time.Duration) *TaskHandle {
	if s == nil || s.closed.Load() || s.manager == nil {
		return nil
	}

	m := s.manager

	// Get or create metadata for this task type
	taskType := reflect.TypeOf(task)
	meta := m.getTaskMeta(taskType)
	if meta == nil {
		// Task type not registered - analyze on the fly
		var err error
		meta, err = analyzeSystem(taskType, nil, m.registry)
		if err != nil {
			return nil
		}
	}

	// Find the bundle for this task
	var bundle *Bundle
	for _, b := range m.bundles {
		if b.getTaskMeta(taskType) != nil {
			bundle = b
			break
		}
	}

	scheduled := &scheduledTask{
		executeAt: time.Now().Add(delay),
		sessions:  []*Session{s},
		task:      task,
		meta:      meta,
		bundle:    bundle,
	}

	s.addTask(scheduled)
	m.taskQueue.Push(scheduled)

	return &TaskHandle{task: scheduled}
}

// Schedule2 schedules a multi-session task for execution after the given delay.
// Both sessions must be in the same world at execution time and belong to the same manager.
// Returns a TaskHandle that can be used to cancel the task.
func Schedule2(s1, s2 *Session, task Runnable, delay time.Duration) *TaskHandle {
	if s1 == nil || s2 == nil || s1.closed.Load() || s2.closed.Load() || s1.manager == nil {
		return nil
	}

	// Both sessions must belong to the same manager
	if s1.manager != s2.manager {
		return nil
	}

	m := s1.manager

	taskType := reflect.TypeOf(task)
	meta := m.getTaskMeta(taskType)
	if meta == nil {
		var err error
		meta, err = analyzeSystem(taskType, nil, m.registry)
		if err != nil {
			return nil
		}
		meta.IsMultiSession = true
	}

	var bundle *Bundle
	for _, b := range m.bundles {
		if b.getTaskMeta(taskType) != nil {
			bundle = b
			break
		}
	}

	scheduled := &scheduledTask{
		executeAt: time.Now().Add(delay),
		sessions:  []*Session{s1, s2},
		task:      task,
		meta:      meta,
		bundle:    bundle,
	}

	s1.addTask(scheduled)
	s2.addTask(scheduled)
	m.taskQueue.Push(scheduled)

	return &TaskHandle{task: scheduled}
}

// Dispatch immediately executes a task in the next tick.
// Returns a TaskHandle that can be used to cancel the task.
func Dispatch(s *Session, task Runnable) *TaskHandle {
	return Schedule(s, task, 0)
}

// Dispatch2 immediately executes a multi-session task in the next tick.
// Returns a TaskHandle that can be used to cancel the task.
func Dispatch2(s1, s2 *Session, task Runnable) *TaskHandle {
	return Schedule2(s1, s2, task, 0)
}

// ScheduleAt schedules a task for execution at a specific time.
// If the time is in the past, the task will execute on the next tick.
// Returns a TaskHandle that can be used to cancel the task.
func ScheduleAt(s *Session, task Runnable, at time.Time) *TaskHandle {
	if s == nil || s.closed.Load() || s.manager == nil {
		return nil
	}

	m := s.manager

	taskType := reflect.TypeOf(task)
	meta := m.getTaskMeta(taskType)
	if meta == nil {
		var err error
		meta, err = analyzeSystem(taskType, nil, m.registry)
		if err != nil {
			return nil
		}
	}

	var bundle *Bundle
	for _, b := range m.bundles {
		if b.getTaskMeta(taskType) != nil {
			bundle = b
			break
		}
	}

	scheduled := &scheduledTask{
		executeAt: at,
		sessions:  []*Session{s},
		task:      task,
		meta:      meta,
		bundle:    bundle,
	}

	s.addTask(scheduled)
	m.taskQueue.Push(scheduled)

	return &TaskHandle{task: scheduled}
}

// RepeatingTaskHandle allows cancelling a repeating scheduled task.
type RepeatingTaskHandle struct {
	cancelled atomic.Bool
	session   *Session
}

// Cancel cancels the repeating task, preventing future executions.
func (h *RepeatingTaskHandle) Cancel() {
	if h != nil {
		h.cancelled.Store(true)
	}
}

// repeatingTaskWrapper wraps a task to reschedule itself after execution.
type repeatingTaskWrapper struct {
	inner     Runnable
	interval  time.Duration
	remaining int // -1 for infinite
	handle    *RepeatingTaskHandle
	meta      *SystemMeta
	bundle    *Bundle
}

func (w *repeatingTaskWrapper) Run(tx *world.Tx) {
	// Check if cancelled
	if w.handle.cancelled.Load() {
		return
	}

	// Execute the inner task
	w.inner.Run(tx)

	// Check if we should reschedule
	if w.handle.cancelled.Load() {
		return
	}

	if w.remaining > 0 {
		w.remaining--
	}

	if w.remaining == 0 {
		return // No more executions
	}

	// Reschedule
	s := w.handle.session
	if s == nil || s.closed.Load() || s.manager == nil {
		return
	}

	scheduled := &scheduledTask{
		executeAt: time.Now().Add(w.interval),
		sessions:  []*Session{s},
		task:      w,
		meta:      w.meta,
		bundle:    w.bundle,
	}

	s.addTask(scheduled)
	s.manager.taskQueue.Push(scheduled)
}

// ScheduleRepeating schedules a task to run repeatedly at the given interval.
// If times is -1, the task repeats indefinitely until cancelled.
// If times is > 0, the task runs exactly that many times.
// Returns a RepeatingTaskHandle that can be used to cancel future executions.
func ScheduleRepeating(s *Session, task Runnable, interval time.Duration, times int) *RepeatingTaskHandle {
	if s == nil || s.closed.Load() || s.manager == nil {
		return nil
	}
	if times == 0 {
		return nil
	}

	m := s.manager

	taskType := reflect.TypeOf(task)
	meta := m.getTaskMeta(taskType)
	if meta == nil {
		var err error
		meta, err = analyzeSystem(taskType, nil, m.registry)
		if err != nil {
			return nil
		}
	}

	var bundle *Bundle
	for _, b := range m.bundles {
		if b.getTaskMeta(taskType) != nil {
			bundle = b
			break
		}
	}

	handle := &RepeatingTaskHandle{
		session: s,
	}

	wrapper := &repeatingTaskWrapper{
		inner:     task,
		interval:  interval,
		remaining: times,
		handle:    handle,
		meta:      meta,
		bundle:    bundle,
	}

	scheduled := &scheduledTask{
		executeAt: time.Now().Add(interval),
		sessions:  []*Session{s},
		task:      wrapper,
		meta:      meta,
		bundle:    bundle,
	}

	s.addTask(scheduled)
	m.taskQueue.Push(scheduled)

	return handle
}
