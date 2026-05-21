package tsdb

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/tsdb/chunkenc"
)

// TestOpenCreatesDB verifies that Open creates a TSDB at the given path with
// the specified retention and that the returned Store is usable.
func TestOpenCreatesDB(t *testing.T) {
	dir := t.TempDir()
	retention := 30 * 24 * time.Hour // 30 days

	store, err := Open(dir, retention)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})

	// The store should expose a non-nil Appender and Querier.
	app := store.Appender(context.Background())
	if app == nil {
		t.Fatal("Appender returned nil")
	}
	// Rollback to avoid dangling state.
	if err := app.Rollback(); err != nil {
		t.Errorf("Rollback: %v", err)
	}
}

// TestOpenEmptyPath rejects an empty directory path.
func TestOpenEmptyPath(t *testing.T) {
	if _, err := Open("", 30*24*time.Hour); err == nil {
		t.Fatal("Open(\"\") = nil error, want error")
	}
}

// TestAppendAndQuery opens a temp TSDB, appends one sample, and queries it
// back to verify round-trip correctness.
func TestAppendAndQuery(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(dir, 30*24*time.Hour)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})

	now := time.Now()
	ts := now.UnixMilli()
	series := labels.FromStrings("monitor_id", "test-01", "monitor_type", "http")

	app := store.Appender(context.Background())
	_, err = app.Append(0, series, ts, 1.0)
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := app.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	// Query back.
	q, err := store.Querier(math.MinInt64, math.MaxInt64)
	if err != nil {
		t.Fatalf("Querier: %v", err)
	}
	defer q.Close()

	ss := q.Select(context.Background(), false, nil,
		labels.MustNewMatcher(labels.MatchEqual, "monitor_id", "test-01"),
	)

	var gotSamples int
	var gotValue float64
	for ss.Next() {
		it := ss.At().Iterator(nil)
		for it.Next() == chunkenc.ValFloat {
			_, v := it.At()
			gotValue = v
			gotSamples++
		}
		if err := it.Err(); err != nil {
			t.Fatalf("iterator error: %v", err)
		}
	}
	if err := ss.Err(); err != nil {
		t.Fatalf("Select error: %v", err)
	}

	if gotSamples != 1 {
		t.Fatalf("got %d samples, want 1", gotSamples)
	}
	if gotValue != 1.0 {
		t.Errorf("got value %f, want 1.0", gotValue)
	}
}

// TestCloseAndReopen verifies that data survives a close-and-reopen cycle.
func TestCloseAndReopen(t *testing.T) {
	dir := t.TempDir()
	retention := 30 * 24 * time.Hour

	// Open, append, close.
	store, err := Open(dir, retention)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	now := time.Now()
	ts := now.UnixMilli()
	series := labels.FromStrings("monitor_id", "persist-01", "monitor_type", "http")

	app := store.Appender(context.Background())
	_, err = app.Append(0, series, ts, 42.0)
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := app.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Reopen the same directory.
	store2, err := Open(dir, retention)
	if err != nil {
		t.Fatalf("Reopen: %v", err)
	}
	t.Cleanup(func() {
		if err := store2.Close(); err != nil {
			t.Errorf("Close(2): %v", err)
		}
	})

	q, err := store2.Querier(math.MinInt64, math.MaxInt64)
	if err != nil {
		t.Fatalf("Querier: %v", err)
	}
	defer q.Close()

	ss := q.Select(context.Background(), false, nil,
		labels.MustNewMatcher(labels.MatchEqual, "monitor_id", "persist-01"),
	)

	var gotSamples int
	var gotValue float64
	for ss.Next() {
		it := ss.At().Iterator(nil)
		for it.Next() == chunkenc.ValFloat {
			_, v := it.At()
			gotValue = v
			gotSamples++
		}
		if err := it.Err(); err != nil {
			t.Fatalf("iterator error: %v", err)
		}
	}
	if err := ss.Err(); err != nil {
		t.Fatalf("Select error: %v", err)
	}

	if gotSamples != 1 {
		t.Fatalf("got %d samples, want 1", gotSamples)
	}
	if gotValue != 42.0 {
		t.Errorf("got value %f, want 42.0", gotValue)
	}
}

// TestClose confirms a clean close of an opened TSDB.
func TestClose(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(dir, 30*24*time.Hour)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// TestCleanup confirms Cleanup runs successfully against an empty store and
// against a store that has had samples appended. Cleanup is what the periodic
// retention loop calls (SPEC §14.6); if it can't run without error the service
// would log warnings forever, so this guards the basic contract.
func TestCleanup(t *testing.T) {
	store := openStore(t)

	if err := store.Cleanup(context.Background()); err != nil {
		t.Fatalf("Cleanup on empty store: %v", err)
	}

	// Append a sample and Cleanup again to confirm it remains safe with data
	// in the head block.
	app := store.Appender(context.Background())
	if _, err := app.Append(0, labels.FromStrings("__name__", "x", "monitor_id", "m"),
		time.Now().UnixMilli(), 1.0); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := app.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	if err := store.Cleanup(context.Background()); err != nil {
		t.Fatalf("Cleanup after append: %v", err)
	}
}
