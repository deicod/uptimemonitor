package scheduler_test

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/deicod/uptimemonitor/internal/monitor"
	"github.com/deicod/uptimemonitor/internal/scheduler"
)

// enabledMonitor builds a monitor configured to tick at interval. Tests use
// short intervals (single-digit ms) to keep total runtime bounded while still
// exercising real time.Ticker behaviour — the scheduler is fundamentally
// time-driven and a fake clock would not catch races against the real ticker.
func enabledMonitor(id string, interval time.Duration) monitor.Monitor {
	return monitor.Monitor{ID: id, Enabled: true, Interval: interval}
}

// waitUntil polls cond every step until it is true or the deadline elapses.
// It avoids time.Sleep-based assertions, which either flake or pad runtime.
func waitUntil(t *testing.T, cond func() bool, timeout time.Duration, msg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", msg)
}

// TestScheduler_FiresOnInterval is the baseline check that a registered,
// enabled monitor is invoked repeatedly. If this breaks, no other scheduler
// behaviour matters.
func TestScheduler_FiresOnInterval(t *testing.T) {
	var count int32
	s := scheduler.New(func(_ context.Context, _ monitor.Monitor, _ bool) {
		atomic.AddInt32(&count, 1)
	}, 4)
	s.Start(context.Background())
	defer s.Stop()

	s.Add(enabledMonitor("m1", 5*time.Millisecond))

	waitUntil(t, func() bool { return atomic.LoadInt32(&count) >= 3 }, time.Second,
		"at least 3 ticks for m1")
}

// TestScheduler_RespectsWorkerBound verifies the bounded worker pool. With N
// workers and many monitors firing simultaneously, never more than N checks
// should be in flight at once.
func TestScheduler_RespectsWorkerBound(t *testing.T) {
	const workers = 2
	const monitors = 10

	release := make(chan struct{})
	var running, maxRunning int32

	s := scheduler.New(func(_ context.Context, _ monitor.Monitor, _ bool) {
		r := atomic.AddInt32(&running, 1)
		// Track the high-water mark of concurrent runners.
		for {
			cur := atomic.LoadInt32(&maxRunning)
			if r <= cur || atomic.CompareAndSwapInt32(&maxRunning, cur, r) {
				break
			}
		}
		<-release
		atomic.AddInt32(&running, -1)
	}, workers)
	s.Start(context.Background())
	t.Cleanup(func() {
		close(release)
		s.Stop()
	})

	for i := 0; i < monitors; i++ {
		s.Add(enabledMonitor(fmt.Sprintf("m%d", i), 5*time.Millisecond))
	}

	// Once the pool is saturated the worker count stops growing; wait for it
	// to reach the bound, then confirm it doesn't exceed it.
	waitUntil(t, func() bool { return atomic.LoadInt32(&maxRunning) == workers }, time.Second,
		"worker pool to saturate")
	// Give the over-fire potential time to manifest; if more than `workers`
	// goroutines were going to run, they would have by now.
	time.Sleep(50 * time.Millisecond)
	if got := atomic.LoadInt32(&maxRunning); got > workers {
		t.Fatalf("maxRunning=%d exceeds worker bound %d", got, workers)
	}
}

// TestScheduler_NoOverlapPerMonitor exercises SPEC §16.3: a still-running
// check for monitor X causes the next interval tick for X to be skipped.
func TestScheduler_NoOverlapPerMonitor(t *testing.T) {
	release := make(chan struct{})
	var concurrent, maxConcurrent int32
	var started int32

	s := scheduler.New(func(_ context.Context, _ monitor.Monitor, _ bool) {
		atomic.AddInt32(&started, 1)
		c := atomic.AddInt32(&concurrent, 1)
		for {
			cur := atomic.LoadInt32(&maxConcurrent)
			if c <= cur || atomic.CompareAndSwapInt32(&maxConcurrent, cur, c) {
				break
			}
		}
		<-release
		atomic.AddInt32(&concurrent, -1)
	}, 4)
	s.Start(context.Background())
	t.Cleanup(func() {
		close(release)
		s.Stop()
	})

	s.Add(enabledMonitor("m1", 5*time.Millisecond))

	// Wait until at least one check is in flight, then let several intervals
	// elapse. Without the no-overlap rule, more goroutines would pile up.
	waitUntil(t, func() bool { return atomic.LoadInt32(&started) >= 1 }, time.Second,
		"first check to start")
	time.Sleep(50 * time.Millisecond)

	if got := atomic.LoadInt32(&maxConcurrent); got != 1 {
		t.Fatalf("maxConcurrent for one monitor = %d, want 1", got)
	}
}

// TestScheduler_ManualTriggerRunsDisabledMonitor exercises SPEC §16.4:
// manual checks run for disabled monitors. The ticker is not started for a
// disabled monitor, so the only way a check fires is through ManualTrigger.
func TestScheduler_ManualTriggerRunsDisabledMonitor(t *testing.T) {
	var manualCalls, scheduledCalls int32
	done := make(chan struct{})

	s := scheduler.New(func(_ context.Context, _ monitor.Monitor, manual bool) {
		if manual {
			atomic.AddInt32(&manualCalls, 1)
			close(done)
		} else {
			atomic.AddInt32(&scheduledCalls, 1)
		}
	}, 2)
	s.Start(context.Background())
	defer s.Stop()

	s.Add(monitor.Monitor{ID: "m1", Enabled: false, Interval: time.Hour})

	if !s.ManualTrigger("m1") {
		t.Fatal("expected ManualTrigger to enqueue")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("manual check did not run within 1s")
	}
	// A disabled monitor must not also be scheduled.
	if got := atomic.LoadInt32(&scheduledCalls); got != 0 {
		t.Fatalf("scheduledCalls=%d, want 0 for disabled monitor", got)
	}
	if got := atomic.LoadInt32(&manualCalls); got != 1 {
		t.Fatalf("manualCalls=%d, want 1", got)
	}
}

// TestScheduler_ManualTriggerUnknownMonitorReturnsFalse documents the
// contract that ManualTrigger reports queueing failure for unknown IDs so
// the IPC handler (M7.7) can map it to a not_found error.
func TestScheduler_ManualTriggerUnknownMonitorReturnsFalse(t *testing.T) {
	s := scheduler.New(func(context.Context, monitor.Monitor, bool) {}, 1)
	s.Start(context.Background())
	defer s.Stop()

	if s.ManualTrigger("missing") {
		t.Fatal("ManualTrigger on unknown id should return false")
	}
}

// TestScheduler_UpdateAppliesNewInterval verifies that an in-place schedule
// update replaces the running ticker, so a slow-interval monitor can be
// re-scheduled to a fast one (and vice versa) without re-adding it.
func TestScheduler_UpdateAppliesNewInterval(t *testing.T) {
	var mu sync.Mutex
	var calls []time.Time

	s := scheduler.New(func(_ context.Context, _ monitor.Monitor, _ bool) {
		mu.Lock()
		calls = append(calls, time.Now())
		mu.Unlock()
	}, 2)
	s.Start(context.Background())
	defer s.Stop()

	// Initial interval is long enough that it would never tick within the
	// test budget.
	s.Add(enabledMonitor("m1", time.Hour))
	time.Sleep(20 * time.Millisecond)
	mu.Lock()
	initial := len(calls)
	mu.Unlock()
	if initial != 0 {
		t.Fatalf("baseline calls=%d, want 0 before update", initial)
	}

	// Now flip to a tight interval — the old ticker must be replaced.
	s.Update(enabledMonitor("m1", 5*time.Millisecond))

	waitUntil(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(calls) >= 2
	}, time.Second, "ticks after interval update")
}

// TestScheduler_RemoveStopsTicker confirms a removed monitor no longer fires
// — a critical invariant for soft-deletes (a deleted monitor should not keep
// generating checks).
func TestScheduler_RemoveStopsTicker(t *testing.T) {
	var count int32
	s := scheduler.New(func(_ context.Context, _ monitor.Monitor, _ bool) {
		atomic.AddInt32(&count, 1)
	}, 2)
	s.Start(context.Background())
	defer s.Stop()

	s.Add(enabledMonitor("m1", 5*time.Millisecond))
	waitUntil(t, func() bool { return atomic.LoadInt32(&count) >= 1 }, time.Second,
		"first tick before remove")

	s.Remove("m1")
	snapshot := atomic.LoadInt32(&count)
	time.Sleep(50 * time.Millisecond) // plenty of intervals
	if got := atomic.LoadInt32(&count); got > snapshot {
		t.Fatalf("count grew from %d to %d after Remove", snapshot, got)
	}
}

// TestScheduler_DisableStopsTicker verifies that updating a monitor to
// disabled stops its ticker, while leaving the entry available for manual
// triggers (the next test confirms the second half).
func TestScheduler_DisableStopsTicker(t *testing.T) {
	var count int32
	s := scheduler.New(func(_ context.Context, _ monitor.Monitor, _ bool) {
		atomic.AddInt32(&count, 1)
	}, 2)
	s.Start(context.Background())
	defer s.Stop()

	s.Add(enabledMonitor("m1", 5*time.Millisecond))
	waitUntil(t, func() bool { return atomic.LoadInt32(&count) >= 1 }, time.Second,
		"first scheduled tick")

	s.Update(monitor.Monitor{ID: "m1", Enabled: false, Interval: 5 * time.Millisecond})
	snapshot := atomic.LoadInt32(&count)
	time.Sleep(50 * time.Millisecond)
	if got := atomic.LoadInt32(&count); got > snapshot+1 {
		// Allow at most one stray tick that was already queued before the
		// Update took effect; growth beyond that means the ticker survived.
		t.Fatalf("count grew from %d to %d after Disable", snapshot, got)
	}
}
