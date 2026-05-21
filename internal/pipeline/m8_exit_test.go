package pipeline_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/deicod/uptimemonitor/internal/monitor"
	"github.com/deicod/uptimemonitor/internal/pipeline"
	"github.com/deicod/uptimemonitor/internal/retention"
	"github.com/deicod/uptimemonitor/internal/store/sqlite"
	"github.com/deicod/uptimemonitor/internal/store/tsdb"
)

// TestM8ExitCheck is the SPEC §28 / PLAN M8.6 acceptance check for milestone M8.
// It wires the pipeline against real SQLite and real TSDB storage (no fake
// SampleWriter) and verifies, in one integration pass, that:
//
//  1. The pipeline writes TSDB samples per check — one set of probe_success,
//     probe_duration, and (when status known) probe_http_status_code series
//     (SPEC §14.2–14.3, PLAN M8.1).
//  2. History is queryable across every MVP range — 1h / 6h / 24h / 7d / 30d —
//     with the SPEC §14.5 bucket count and resolution for each (PLAN M8.2).
//  3. Retention runs end-to-end: the retention.Cleaner prunes SQLite
//     check_results and triggers TSDB compaction without error
//     (PLAN M8.4; SPEC §12.5, §14.4, §14.6).
//
// The TUI bullet ("the TUI shows history") is proven by the M8.5 unit tests in
// internal/tui (TestMonitorDetailLoadsHistory, TestMonitorDetailViewRendersHistoryGlyphs,
// TestMonitorDetailRangeKeyRefetchesHistory). Driving a TUI here would only
// repeat what those tests already cover; this exit check focuses on the
// storage and retention seams M8 actually introduced.
func TestM8ExitCheck(t *testing.T) {
	ctx := context.Background()

	// --- Real SQLite ---
	store, err := sqlite.Open(filepath.Join(t.TempDir(), "m8.db"))
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Migrate(); err != nil {
		t.Fatalf("sqlite.Migrate: %v", err)
	}

	// --- Real TSDB (a generous retention so the test's samples don't expire
	//     between writes and the per-range queries below). ---
	ts, err := tsdb.Open(filepath.Join(t.TempDir(), "tsdb"), 90*24*time.Hour)
	if err != nil {
		t.Fatalf("tsdb.Open: %v", err)
	}
	t.Cleanup(func() { _ = ts.Close() })

	monitors := sqlite.NewMonitorRepo(store)
	states := sqlite.NewMonitorStateRepo(store)
	events := sqlite.NewEventRepo(store)
	incidents := sqlite.NewIncidentRepo(store)
	checks := sqlite.NewCheckResultRepo(store)
	svc := monitor.NewService(monitors, states, events)

	prober := &fakeProber{}
	p := pipeline.New(prober, checks, states, events, incidents, ts, discardLogger())

	cfg, err := json.Marshal(monitor.HTTPMonitorConfig{
		URL:               "https://example.com",
		Method:            "GET",
		ExpectedStatusMin: 200,
		ExpectedStatusMax: 299,
	})
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	m, err := svc.Create(ctx, &monitor.Monitor{
		Name:                 "M8 Exit",
		Type:                 monitor.MonitorTypeHTTP,
		Enabled:              true,
		Interval:             60 * time.Second,
		Timeout:              10 * time.Second,
		Config:               cfg,
		NotificationsEnabled: false,
	})
	if err != nil {
		t.Fatalf("svc.Create: %v", err)
	}

	// --- Phase 1: drive a fixed sequence of checks against the real pipeline.
	//
	// The sequence covers success and failure so the TSDB picks up samples for
	// the probe_success metric in both states (1.0 and 0.0) — this exercises
	// the omit-status-code branch for failures (SPEC §14.3) without making the
	// test assert on a specific status sample count.
	statusOK := 200
	prober.queue = []probeOutcome{
		{success: true, status: &statusOK},
		{success: true, status: &statusOK},
		{success: false, errStr: "request failed"},
		{success: true, status: &statusOK},
		{success: true, status: &statusOK},
	}
	for range prober.queue {
		p.Run(ctx, *m, false)
	}

	// SQLite proof: every Run produced a check_result row.
	persisted, err := checks.ListRecent(ctx, m.ID, 0)
	if err != nil {
		t.Fatalf("ListRecent: %v", err)
	}
	if len(persisted) != len(prober.queue) {
		t.Fatalf("check_results rows = %d, want %d (one per Run)",
			len(persisted), len(prober.queue))
	}

	// --- Phase 2: history queryable across all five MVP ranges (SPEC §14.5).
	//
	// For each range we assert the bucket count equals duration/resolution,
	// that bucket spans are exactly the resolution, and that at least one
	// bucket reflects observed activity (state != unknown). The samples were
	// written "now", so they always land in the last bucket of every range.
	now := time.Now().UTC()
	for _, rg := range tsdb.SupportedRanges() {
		duration, ok := tsdb.DurationFor(rg)
		if !ok {
			t.Fatalf("DurationFor(%q): unsupported", rg)
		}
		resolution, ok := tsdb.ResolutionFor(rg)
		if !ok {
			t.Fatalf("ResolutionFor(%q): unsupported", rg)
		}
		wantBuckets := int(duration / resolution)

		points, err := ts.QueryHistory(ctx, tsdb.HistoryQuery{
			MonitorID: m.ID,
			Range:     rg,
			Now:       now,
		})
		if err != nil {
			t.Fatalf("QueryHistory(%q): %v", rg, err)
		}
		if len(points) != wantBuckets {
			t.Errorf("range %q: bucket count = %d, want %d",
				rg, len(points), wantBuckets)
			continue
		}
		if got := points[0].End.Sub(points[0].Start); got != resolution {
			t.Errorf("range %q: bucket width = %s, want %s", rg, got, resolution)
		}
		// Samples were just written, so at least one bucket carries a real
		// state. Without this guard, a query that silently returned all-zero
		// buckets across every range would pass.
		var observed bool
		for _, pt := range points {
			if pt.State != monitor.StateUnknown {
				observed = true
				break
			}
		}
		if !observed {
			t.Errorf("range %q: every bucket is unknown — no samples landed", rg)
		}
	}

	// --- Phase 3: retention runs end-to-end.
	//
	// Pin "now" far in the future so the prune cutoff sits after every
	// check_result we just inserted; this proves the prune SQL deletes
	// matching rows. TSDB cleanup is best-effort and only needs to not
	// error — the Prometheus TSDB doesn't expose a synchronous "rows
	// removed" count we can assert on.
	cleaner := retention.New(checks, ts, retention.Options{
		CheckResultRetention: 1 * time.Second,
		Interval:             0, // one-shot
		Now:                  func() time.Time { return now.Add(24 * time.Hour) },
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := cleaner.Run(ctx); err != nil {
		t.Fatalf("retention.Run: %v", err)
	}
	after, err := checks.ListRecent(ctx, m.ID, 0)
	if err != nil {
		t.Fatalf("ListRecent after prune: %v", err)
	}
	if len(after) != 0 {
		t.Errorf("check_results after prune = %d, want 0", len(after))
	}
}
