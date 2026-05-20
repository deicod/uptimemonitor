package scheduler

// worker pulls jobs from the queue, runs the check, and clears the
// per-monitor running flag so the next tick can proceed. Workers are
// interchangeable: jobs are distributed by Go's runtime, and SPEC §16.2's
// bound is enforced by sizing the goroutine pool, not by per-worker state.
func (s *Scheduler) worker() {
	defer s.workerWg.Done()
	for {
		select {
		case <-s.ctx.Done():
			return
		case j := <-s.jobs:
			s.check(s.ctx, j.monitor, j.manual)
			s.clearRunning(j.monitor.ID)
		}
	}
}

// clearRunning resets the no-overlap flag. The entry may have been removed
// or replaced while the worker was busy; in either case the flag on the
// current entry (if any) is the one a future tick will check, so a stale
// running flag on a removed entry harmlessly disappears with it.
func (s *Scheduler) clearRunning(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.entries[id]; ok {
		e.running.Store(false)
	}
}
