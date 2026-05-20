// Package scheduler runs the periodic check loop for all enabled monitors
// (SPEC §16). It dispatches jobs to a bounded worker pool, enforces the
// no-overlap rule per monitor, and accepts dynamic add/update/remove and
// enable/disable signals so the monitor.Service can drive it through its
// OnChange observer (wired in M7.6).
package scheduler

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/deicod/uptimemonitor/internal/monitor"
)

// CheckFunc is invoked once per check the scheduler runs. It owns probe
// execution and persistence; the scheduler only decides when to call it and
// guarantees no two concurrent calls share the same monitor ID.
//
// The manual flag is true when the call originated from ManualTrigger. The
// pipeline (M7.6) keys off it to honour SPEC §16.4: a manual check may run on
// a disabled monitor without flipping its paused state.
type CheckFunc func(ctx context.Context, m monitor.Monitor, manual bool)

// Scheduler is the runtime engine for periodic monitor checks. Build one with
// New, then Start to launch workers. After Start, monitors are registered
// with Add/Update and forgotten with Remove. Stop releases all resources.
type Scheduler struct {
	check   CheckFunc
	workers int

	jobs chan job

	mu      sync.Mutex
	entries map[string]*entry

	ctx      context.Context
	cancel   context.CancelFunc
	tickerWg sync.WaitGroup
	workerWg sync.WaitGroup
	started  atomic.Bool
	stopped  atomic.Bool
}

// entry tracks one registered monitor. The running flag enforces the
// no-overlap rule (SPEC §16.3) atomically across the ticker that wants to
// enqueue a job and the worker that clears it on completion.
type entry struct {
	monitor monitor.Monitor
	cancel  context.CancelFunc
	running atomic.Bool
}

// job is the unit of work the workers pull from the queue. It carries a
// snapshot of the monitor as it was when the timer fired so updates after
// enqueue do not retroactively change the executing check.
type job struct {
	monitor monitor.Monitor
	manual  bool
}

// New returns a scheduler that calls check for every probe and uses up to
// workers concurrent worker goroutines. A workers value of zero or less is
// clamped to one so callers can pass an unvalidated config field directly.
func New(check CheckFunc, workers int) *Scheduler {
	if workers < 1 {
		workers = 1
	}
	return &Scheduler{
		check:   check,
		workers: workers,
		entries: make(map[string]*entry),
		// A small headroom over `workers` lets a burst of manual triggers
		// land without blocking the caller; sustained overflow blocks rather
		// than drops, because silently discarding a user-initiated check
		// would violate the contract of ManualTrigger.
		jobs: make(chan job, workers*2),
	}
}

// Start launches the worker pool. It must be called before any Add or
// ManualTrigger and is a no-op on a scheduler that has already been started.
func (s *Scheduler) Start(ctx context.Context) {
	if !s.started.CompareAndSwap(false, true) {
		return
	}
	s.ctx, s.cancel = context.WithCancel(ctx)
	for i := 0; i < s.workers; i++ {
		s.workerWg.Add(1)
		go s.worker()
	}
}

// Stop cancels all per-monitor tickers, waits for them to exit, then waits
// for the workers to finish their current jobs. After Stop the scheduler
// cannot be restarted.
func (s *Scheduler) Stop() {
	if !s.started.Load() || !s.stopped.CompareAndSwap(false, true) {
		return
	}
	s.cancel()
	// Ticker goroutines exit on ctx.Done; wait for them before draining
	// workers so no new jobs can arrive after we declare shutdown done.
	s.tickerWg.Wait()
	s.workerWg.Wait()

	s.mu.Lock()
	s.entries = make(map[string]*entry)
	s.mu.Unlock()
}

// Add registers a monitor and, if it is enabled, starts its interval timer.
// Re-adding an existing ID replaces the prior registration; callers who want
// to express intent can use Update, which is an alias for Add.
func (s *Scheduler) Add(m monitor.Monitor) {
	s.upsert(m)
}

// Update reapplies a monitor's interval and config. It replaces the current
// ticker (so a slow→fast interval change takes effect on the next tick).
func (s *Scheduler) Update(m monitor.Monitor) {
	s.upsert(m)
}

// Remove stops scheduling and forgets the monitor. A subsequent
// ManualTrigger for the same ID returns false.
func (s *Scheduler) Remove(id string) {
	s.mu.Lock()
	e, ok := s.entries[id]
	delete(s.entries, id)
	s.mu.Unlock()
	if ok {
		e.cancel()
	}
}

// ManualTrigger enqueues an out-of-band check for the named monitor. It
// returns false if the monitor is unknown, if a check is already running for
// it (no-overlap rule applies to manual checks too), or if the scheduler is
// shutting down. Manual checks run on disabled monitors (SPEC §16.4) — the
// pipeline is responsible for not unpausing them.
func (s *Scheduler) ManualTrigger(id string) bool {
	return s.fire(id, true)
}

// upsert installs or replaces an entry. It cancels any previous ticker first
// to avoid two tickers running for the same monitor.
func (s *Scheduler) upsert(m monitor.Monitor) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if old, ok := s.entries[m.ID]; ok {
		old.cancel()
		// Preserve the running flag so an in-flight check still blocks new
		// enqueues for this monitor until the worker clears it.
		e := &entry{monitor: m, cancel: func() {}}
		e.running.Store(old.running.Load())
		s.entries[m.ID] = e
	} else {
		s.entries[m.ID] = &entry{monitor: m, cancel: func() {}}
	}
	if !s.started.Load() || s.stopped.Load() {
		return
	}
	if !shouldSchedule(m) {
		return
	}
	tickCtx, cancel := context.WithCancel(s.ctx)
	s.entries[m.ID].cancel = cancel
	s.tickerWg.Add(1)
	go s.runTicker(tickCtx, m.ID, m.Interval)
}

// shouldSchedule reports whether a monitor is eligible for periodic ticks.
// A non-positive interval is rejected defensively; validation upstream
// enforces it, but time.NewTicker panics on d <= 0 and we would rather skip.
func shouldSchedule(m monitor.Monitor) bool {
	return m.Enabled && m.DeletedAt == nil && m.Interval > 0
}

// runTicker is the per-monitor scheduling goroutine. It exits when its
// context is cancelled (via upsert replacing it, Remove, or Stop).
func (s *Scheduler) runTicker(ctx context.Context, id string, interval time.Duration) {
	defer s.tickerWg.Done()
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.fire(id, false)
		}
	}
}

// fire is the common path for scheduled and manual checks. It applies the
// no-overlap rule, snapshots the monitor under lock, and enqueues a job —
// or aborts cleanly on shutdown.
func (s *Scheduler) fire(id string, manual bool) bool {
	s.mu.Lock()
	e, ok := s.entries[id]
	if !ok {
		s.mu.Unlock()
		return false
	}
	// Scheduled ticks for a disabled or deleted monitor are dropped, but
	// manual triggers proceed (SPEC §16.4).
	if !manual && !shouldSchedule(e.monitor) {
		s.mu.Unlock()
		return false
	}
	if !e.running.CompareAndSwap(false, true) {
		s.mu.Unlock()
		return false
	}
	mon := e.monitor
	s.mu.Unlock()

	select {
	case <-s.ctx.Done():
		e.running.Store(false)
		return false
	default:
	}
	select {
	case s.jobs <- job{monitor: mon, manual: manual}:
		return true
	case <-s.ctx.Done():
		e.running.Store(false)
		return false
	}
}
