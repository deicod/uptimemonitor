package retention_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/deicod/uptimemonitor/internal/retention"
)

// fakePruner records each PruneOlderThan call so tests can assert on the
// cutoff and invocation count.
type fakePruner struct {
	calls   int32
	cutoffs []time.Time
	err     error
}

func (f *fakePruner) PruneOlderThan(_ context.Context, cutoff time.Time) (int64, error) {
	f.cutoffs = append(f.cutoffs, cutoff)
	atomic.AddInt32(&f.calls, 1)
	return 0, f.err
}

// fakeTSDB records Cleanup calls.
type fakeTSDB struct {
	calls int32
	err   error
}

func (f *fakeTSDB) Cleanup(_ context.Context) error {
	atomic.AddInt32(&f.calls, 1)
	return f.err
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// TestCleanerRun_PrunesUsingRetentionCutoff verifies that Run computes the
// cutoff as now-retention and forwards both prune and tsdb cleanup. The cutoff
// must come from Now() so tests are deterministic — drift here would silently
// retain too much or too little data.
func TestCleanerRun_PrunesUsingRetentionCutoff(t *testing.T) {
	now := time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC)
	pruner := &fakePruner{}
	ts := &fakeTSDB{}

	c := retention.New(pruner, ts, retention.Options{
		CheckResultRetention: 30 * 24 * time.Hour,
		Now:                  func() time.Time { return now },
	}, discardLogger())

	if err := c.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if pruner.calls != 1 {
		t.Errorf("prune calls = %d, want 1", pruner.calls)
	}
	if ts.calls != 1 {
		t.Errorf("tsdb cleanup calls = %d, want 1", ts.calls)
	}
	want := now.Add(-30 * 24 * time.Hour)
	if !pruner.cutoffs[0].Equal(want) {
		t.Errorf("cutoff = %v, want %v", pruner.cutoffs[0], want)
	}
}

// TestCleanerRun_ContinuesAfterPruneFailure ensures a SQLite error does not
// stop the TSDB compaction. Without this guarantee a transient SQLite hiccup
// would let the TSDB grow without bound until the next cycle.
func TestCleanerRun_ContinuesAfterPruneFailure(t *testing.T) {
	pruner := &fakePruner{err: errors.New("sqlite down")}
	ts := &fakeTSDB{}

	c := retention.New(pruner, ts, retention.Options{
		CheckResultRetention: time.Hour,
	}, discardLogger())

	err := c.Run(context.Background())
	if err == nil {
		t.Fatal("Run returned nil error despite prune failure")
	}
	if ts.calls != 1 {
		t.Errorf("tsdb cleanup calls = %d, want 1 even after prune failure", ts.calls)
	}
}

// TestCleanerStart_RunsImmediatelyAndOnInterval verifies the periodic loop
// runs the initial pass without waiting for the first tick — operators expect
// retention to start enforcing at startup, not one interval later.
func TestCleanerStart_RunsImmediatelyAndOnInterval(t *testing.T) {
	pruner := &fakePruner{}
	ts := &fakeTSDB{}

	c := retention.New(pruner, ts, retention.Options{
		CheckResultRetention: time.Hour,
		Interval:             10 * time.Millisecond,
	}, discardLogger())

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		c.Start(ctx)
		close(done)
	}()

	// Wait long enough for the initial run plus at least one tick.
	deadline := time.After(500 * time.Millisecond)
	for {
		if atomic.LoadInt32(&pruner.calls) >= 2 && atomic.LoadInt32(&ts.calls) >= 2 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("did not observe two runs: prune=%d tsdb=%d",
				pruner.calls, ts.calls)
		case <-time.After(5 * time.Millisecond):
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Start did not return after ctx cancel")
	}
}

// TestCleanerStart_NoIntervalRunsOnce confirms that a non-positive interval
// runs the startup pass and returns, which is what the service does when
// retention is configured as one-shot in tests.
func TestCleanerStart_NoIntervalRunsOnce(t *testing.T) {
	pruner := &fakePruner{}
	ts := &fakeTSDB{}
	c := retention.New(pruner, ts, retention.Options{
		CheckResultRetention: time.Hour,
		Interval:             0,
	}, discardLogger())

	done := make(chan struct{})
	go func() {
		c.Start(context.Background())
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Start with zero interval did not return")
	}
	if pruner.calls != 1 || ts.calls != 1 {
		t.Errorf("calls: prune=%d tsdb=%d, want 1/1", pruner.calls, ts.calls)
	}
}
