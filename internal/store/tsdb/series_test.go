package tsdb

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/tsdb/chunkenc"
)

// TestWriteCheck_SuccessfulCheckWritesAllSeries verifies that a successful
// check writes all three sample series (success, duration, status) with the
// expected labels and values (SPEC §14.2–14.3).
func TestWriteCheck_SuccessfulCheckWritesAllSeries(t *testing.T) {
	store := openStore(t)

	status := 200
	finishedAt := time.Now().UTC()
	sample := CheckSample{
		MonitorID:      "mon-01",
		MonitorType:    "http",
		FinishedAt:     finishedAt,
		Success:        true,
		Duration:       150 * time.Millisecond,
		HTTPStatusCode: &status,
	}
	if err := store.WriteCheck(context.Background(), sample); err != nil {
		t.Fatalf("WriteCheck: %v", err)
	}

	got := readAllSamples(t, store, "mon-01")

	wantSuccess := 1.0
	if v, ok := got[MetricProbeSuccess]; !ok || v != wantSuccess {
		t.Errorf("%s = (%v, %v), want (%v, true)", MetricProbeSuccess, v, ok, wantSuccess)
	}
	if v, ok := got[MetricProbeDuration]; !ok || v != 0.15 {
		t.Errorf("%s = (%v, %v), want (0.15, true)", MetricProbeDuration, v, ok)
	}
	if v, ok := got[MetricProbeStatus]; !ok || v != 200 {
		t.Errorf("%s = (%v, %v), want (200, true)", MetricProbeStatus, v, ok)
	}
}

// TestWriteCheck_FailedCheckOmitsStatus verifies that a check without an HTTP
// status code (e.g. transport error) writes success and duration but omits the
// status series (SPEC §14.3: "For failed checks without HTTP status, omit").
func TestWriteCheck_FailedCheckOmitsStatus(t *testing.T) {
	store := openStore(t)

	sample := CheckSample{
		MonitorID:      "mon-fail",
		MonitorType:    "http",
		FinishedAt:     time.Now().UTC(),
		Success:        false,
		Duration:       3 * time.Second,
		HTTPStatusCode: nil,
	}
	if err := store.WriteCheck(context.Background(), sample); err != nil {
		t.Fatalf("WriteCheck: %v", err)
	}

	got := readAllSamples(t, store, "mon-fail")

	if v, ok := got[MetricProbeSuccess]; !ok || v != 0 {
		t.Errorf("%s = (%v, %v), want (0, true)", MetricProbeSuccess, v, ok)
	}
	if v, ok := got[MetricProbeDuration]; !ok || v != 3.0 {
		t.Errorf("%s = (%v, %v), want (3.0, true)", MetricProbeDuration, v, ok)
	}
	if _, ok := got[MetricProbeStatus]; ok {
		t.Errorf("%s present for failed check without status; want absent", MetricProbeStatus)
	}
}

// TestWriteCheck_LabelsAttached verifies that the monitor_id and monitor_type
// labels are attached to every series so queries can filter by them.
func TestWriteCheck_LabelsAttached(t *testing.T) {
	store := openStore(t)

	status := 404
	sample := CheckSample{
		MonitorID:      "mon-labels",
		MonitorType:    "http",
		FinishedAt:     time.Now().UTC(),
		Success:        false,
		Duration:       10 * time.Millisecond,
		HTTPStatusCode: &status,
	}
	if err := store.WriteCheck(context.Background(), sample); err != nil {
		t.Fatalf("WriteCheck: %v", err)
	}

	q, err := store.Querier(math.MinInt64, math.MaxInt64)
	if err != nil {
		t.Fatalf("Querier: %v", err)
	}
	defer q.Close()

	ss := q.Select(context.Background(), false, nil,
		labels.MustNewMatcher(labels.MatchEqual, LabelMonitorID, "mon-labels"),
		labels.MustNewMatcher(labels.MatchEqual, LabelMonitorType, "http"),
	)
	var series int
	for ss.Next() {
		series++
		lbls := ss.At().Labels()
		if lbls.Get(LabelMonitorID) != "mon-labels" {
			t.Errorf("monitor_id label = %q, want mon-labels", lbls.Get(LabelMonitorID))
		}
		if lbls.Get(LabelMonitorType) != "http" {
			t.Errorf("monitor_type label = %q, want http", lbls.Get(LabelMonitorType))
		}
	}
	if err := ss.Err(); err != nil {
		t.Fatalf("Select err: %v", err)
	}
	if series != 3 {
		t.Errorf("series count for mon-labels = %d, want 3", series)
	}
}

// openStore opens a fresh TSDB in a temp dir and registers Close for cleanup.
func openStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(t.TempDir(), 30*24*time.Hour)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return store
}

// readAllSamples returns a map from metric name (__name__) to the single
// sample value observed for that series, so tests can assert presence and
// value without iterating manually each time.
func readAllSamples(t *testing.T, store *Store, monitorID string) map[string]float64 {
	t.Helper()
	q, err := store.Querier(math.MinInt64, math.MaxInt64)
	if err != nil {
		t.Fatalf("Querier: %v", err)
	}
	defer q.Close()

	ss := q.Select(context.Background(), false, nil,
		labels.MustNewMatcher(labels.MatchEqual, LabelMonitorID, monitorID),
	)
	out := make(map[string]float64)
	for ss.Next() {
		s := ss.At()
		name := s.Labels().Get(model.MetricNameLabel)
		it := s.Iterator(nil)
		for it.Next() == chunkenc.ValFloat {
			_, v := it.At()
			out[name] = v
		}
		if err := it.Err(); err != nil {
			t.Fatalf("iterator: %v", err)
		}
	}
	if err := ss.Err(); err != nil {
		t.Fatalf("Select: %v", err)
	}
	return out
}
