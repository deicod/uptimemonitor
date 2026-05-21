package retention_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/deicod/uptimemonitor/internal/monitor"
	"github.com/deicod/uptimemonitor/internal/retention"
	"github.com/deicod/uptimemonitor/internal/store/sqlite"
	"github.com/deicod/uptimemonitor/internal/store/tsdb"
)

// TestCleanerRun_RemovesOldCheckResultsKeepsRecent is the end-to-end retention
// guarantee: a row older than the configured retention window must be removed
// and a recent row must survive. Without this assertion the periodic loop
// could silently no-op and grow the database indefinitely.
func TestCleanerRun_RemovesOldCheckResultsKeepsRecent(t *testing.T) {
	dir := t.TempDir()
	sq, err := sqlite.Open(filepath.Join(dir, "config.db"))
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { sq.Close() })
	if err := sq.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	monitorID := insertSampleMonitor(t, sq)
	checks := sqlite.NewCheckResultRepo(sq)
	ctx := context.Background()

	now := time.Now().UTC().Truncate(time.Second)
	old := buildCheck(monitorID, now.Add(-40*24*time.Hour))
	recent := buildCheck(monitorID, now.Add(-1*time.Hour))
	for _, cr := range []*monitor.CheckResult{old, recent} {
		if err := checks.Insert(ctx, cr); err != nil {
			t.Fatalf("Insert: %v", err)
		}
	}

	ts, err := tsdb.Open(filepath.Join(dir, "tsdb"), 30*24*time.Hour)
	if err != nil {
		t.Fatalf("tsdb.Open: %v", err)
	}
	t.Cleanup(func() { ts.Close() })

	c := retention.New(checks, ts, retention.Options{
		CheckResultRetention: 30 * 24 * time.Hour,
		Now:                  func() time.Time { return now },
	}, discardLogger())

	if err := c.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}

	rows, err := checks.ListRecent(ctx, monitorID, 10)
	if err != nil {
		t.Fatalf("ListRecent: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("after retention got %d rows, want 1", len(rows))
	}
	if rows[0].ID != recent.ID {
		t.Errorf("kept row = %s, want recent %s", rows[0].ID, recent.ID)
	}
}

// insertSampleMonitor inserts a minimal HTTP monitor so check_result FKs hold.
func insertSampleMonitor(t *testing.T, store *sqlite.Store) string {
	t.Helper()
	cfg, err := json.Marshal(map[string]any{
		"url":                 "https://example.com",
		"method":              "GET",
		"expected_status_min": 200,
		"expected_status_max": 299,
	})
	if err != nil {
		t.Fatalf("marshal monitor config: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	m := &monitor.Monitor{
		ID:                   monitor.NewID(),
		Name:                 "retention-test",
		Type:                 monitor.MonitorTypeHTTP,
		Enabled:              true,
		Interval:             60 * time.Second,
		Timeout:              10 * time.Second,
		Config:               cfg,
		NotificationsEnabled: true,
		CreatedAt:            now,
		UpdatedAt:            now,
	}
	if err := sqlite.NewMonitorRepo(store).Insert(context.Background(), m); err != nil {
		t.Fatalf("Insert monitor: %v", err)
	}
	return m.ID
}

func buildCheck(monitorID string, startedAt time.Time) *monitor.CheckResult {
	status := 200
	return &monitor.CheckResult{
		ID:             monitor.NewID(),
		MonitorID:      monitorID,
		StartedAt:      startedAt,
		FinishedAt:     startedAt.Add(150 * time.Millisecond),
		Duration:       150 * time.Millisecond,
		Success:        true,
		State:          monitor.StateUp,
		HTTPStatusCode: &status,
	}
}
