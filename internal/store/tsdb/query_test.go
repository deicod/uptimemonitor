package tsdb

import (
	"context"
	"testing"
	"time"

	"github.com/deicod/uptimemonitor/internal/monitor"
)

// TestQueryHistory_BucketCountAndResolutionPerRange asserts the SPEC §14.5
// range-to-resolution mapping by checking each supported range produces the
// expected number of equal-sized buckets ending at q.Now.
func TestQueryHistory_BucketCountAndResolutionPerRange(t *testing.T) {
	store := openStore(t)
	now := time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC)

	cases := []struct {
		r          Range
		duration   time.Duration
		resolution time.Duration
		wantCount  int
	}{
		{Range1h, 1 * time.Hour, 1 * time.Minute, 60},
		{Range6h, 6 * time.Hour, 5 * time.Minute, 72},
		{Range24h, 24 * time.Hour, 15 * time.Minute, 96},
		{Range7d, 7 * 24 * time.Hour, 1 * time.Hour, 168},
		{Range30d, 30 * 24 * time.Hour, 6 * time.Hour, 120},
	}
	for _, c := range cases {
		t.Run(string(c.r), func(t *testing.T) {
			points, err := store.QueryHistory(context.Background(), HistoryQuery{
				MonitorID: "mon-range",
				Range:     c.r,
				Now:       now,
			})
			if err != nil {
				t.Fatalf("QueryHistory: %v", err)
			}
			if len(points) != c.wantCount {
				t.Fatalf("len(points) = %d, want %d", len(points), c.wantCount)
			}
			// All buckets exactly c.resolution wide.
			for i, p := range points {
				if got := p.End.Sub(p.Start); got != c.resolution {
					t.Errorf("bucket %d width = %v, want %v", i, got, c.resolution)
				}
			}
			// Chronological and contiguous, ending at now.
			if !points[len(points)-1].End.Equal(now) {
				t.Errorf("last bucket end = %v, want %v", points[len(points)-1].End, now)
			}
			if got, want := points[0].Start, now.Add(-c.duration); !got.Equal(want) {
				t.Errorf("first bucket start = %v, want %v", got, want)
			}
		})
	}
}

// TestQueryHistory_AggregatesSuccessRatioAndDuration verifies that when
// multiple samples fall in the same bucket, success_ratio is the fraction of
// successful checks and avg_duration_ms is the mean of durations. With a mix
// of success and failure the bucket state must be 'down' (any failure means
// the bucket cannot be considered fully up).
func TestQueryHistory_AggregatesSuccessRatioAndDuration(t *testing.T) {
	store := openStore(t)
	now := time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC)

	// All four samples land in the most recent 1m bucket (now-1m .. now).
	bucketEnd := now
	bucketStart := bucketEnd.Add(-1 * time.Minute)
	samples := []CheckSample{
		{MonitorID: "mon-agg", MonitorType: "http", FinishedAt: bucketStart.Add(10 * time.Second), Success: true, Duration: 100 * time.Millisecond},
		{MonitorID: "mon-agg", MonitorType: "http", FinishedAt: bucketStart.Add(20 * time.Second), Success: true, Duration: 200 * time.Millisecond},
		{MonitorID: "mon-agg", MonitorType: "http", FinishedAt: bucketStart.Add(30 * time.Second), Success: true, Duration: 300 * time.Millisecond},
		{MonitorID: "mon-agg", MonitorType: "http", FinishedAt: bucketStart.Add(40 * time.Second), Success: false, Duration: 400 * time.Millisecond},
	}
	for _, s := range samples {
		if err := store.WriteCheck(context.Background(), s); err != nil {
			t.Fatalf("WriteCheck: %v", err)
		}
	}

	points, err := store.QueryHistory(context.Background(), HistoryQuery{
		MonitorID: "mon-agg",
		Range:     Range1h,
		Now:       now,
	})
	if err != nil {
		t.Fatalf("QueryHistory: %v", err)
	}
	if len(points) != 60 {
		t.Fatalf("len(points) = %d, want 60", len(points))
	}

	last := points[len(points)-1]
	if got, want := last.SuccessRatio, 0.75; got != want {
		t.Errorf("success_ratio = %v, want %v", got, want)
	}
	if got, want := last.AvgDurationMS, int64(250); got != want {
		t.Errorf("avg_duration_ms = %d, want %d", got, want)
	}
	if last.State != monitor.StateDown {
		t.Errorf("state = %q, want %q (mixed bucket with failure)", last.State, monitor.StateDown)
	}
}

// TestQueryHistory_EmptyBucketIsUnknown verifies that a bucket with no samples
// is reported with state=unknown and zero ratio/duration so the TUI can render
// a 'no data' glyph.
func TestQueryHistory_EmptyBucketIsUnknown(t *testing.T) {
	store := openStore(t)
	now := time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC)

	// One success sample in the most recent bucket; all other buckets empty.
	if err := store.WriteCheck(context.Background(), CheckSample{
		MonitorID:   "mon-empty",
		MonitorType: "http",
		FinishedAt:  now.Add(-30 * time.Second),
		Success:     true,
		Duration:    50 * time.Millisecond,
	}); err != nil {
		t.Fatalf("WriteCheck: %v", err)
	}

	points, err := store.QueryHistory(context.Background(), HistoryQuery{
		MonitorID: "mon-empty",
		Range:     Range1h,
		Now:       now,
	})
	if err != nil {
		t.Fatalf("QueryHistory: %v", err)
	}

	last := points[len(points)-1]
	if last.State != monitor.StateUp {
		t.Errorf("last bucket state = %q, want %q", last.State, monitor.StateUp)
	}
	// Every earlier bucket should be unknown with zero ratio/duration.
	for i, p := range points[:len(points)-1] {
		if p.State != monitor.StateUnknown {
			t.Errorf("bucket %d state = %q, want %q", i, p.State, monitor.StateUnknown)
		}
		if p.SuccessRatio != 0 {
			t.Errorf("bucket %d success_ratio = %v, want 0", i, p.SuccessRatio)
		}
		if p.AvgDurationMS != 0 {
			t.Errorf("bucket %d avg_duration_ms = %d, want 0", i, p.AvgDurationMS)
		}
	}
}

// TestQueryHistory_AllSuccessBucketIsUp verifies that a bucket with only
// successful samples has success_ratio=1 and state=up.
func TestQueryHistory_AllSuccessBucketIsUp(t *testing.T) {
	store := openStore(t)
	now := time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC)

	// Append chronologically (TSDB rejects out-of-order samples per series).
	offsets := []time.Duration{-30 * time.Second, -20 * time.Second, -10 * time.Second}
	for _, off := range offsets {
		if err := store.WriteCheck(context.Background(), CheckSample{
			MonitorID:   "mon-up",
			MonitorType: "http",
			FinishedAt:  now.Add(off),
			Success:     true,
			Duration:    100 * time.Millisecond,
		}); err != nil {
			t.Fatalf("WriteCheck: %v", err)
		}
	}

	points, err := store.QueryHistory(context.Background(), HistoryQuery{
		MonitorID: "mon-up",
		Range:     Range1h,
		Now:       now,
	})
	if err != nil {
		t.Fatalf("QueryHistory: %v", err)
	}
	last := points[len(points)-1]
	if last.State != monitor.StateUp {
		t.Errorf("state = %q, want %q", last.State, monitor.StateUp)
	}
	if last.SuccessRatio != 1.0 {
		t.Errorf("success_ratio = %v, want 1.0", last.SuccessRatio)
	}
	if last.AvgDurationMS != 100 {
		t.Errorf("avg_duration_ms = %d, want 100", last.AvgDurationMS)
	}
}

// TestQueryHistory_AllFailureBucketIsDown verifies that a bucket with only
// failed samples has success_ratio=0 and state=down.
func TestQueryHistory_AllFailureBucketIsDown(t *testing.T) {
	store := openStore(t)
	now := time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC)

	offsets := []time.Duration{-20 * time.Second, -10 * time.Second}
	for _, off := range offsets {
		if err := store.WriteCheck(context.Background(), CheckSample{
			MonitorID:   "mon-down",
			MonitorType: "http",
			FinishedAt:  now.Add(off),
			Success:     false,
			Duration:    2 * time.Second,
		}); err != nil {
			t.Fatalf("WriteCheck: %v", err)
		}
	}

	points, err := store.QueryHistory(context.Background(), HistoryQuery{
		MonitorID: "mon-down",
		Range:     Range1h,
		Now:       now,
	})
	if err != nil {
		t.Fatalf("QueryHistory: %v", err)
	}
	last := points[len(points)-1]
	if last.State != monitor.StateDown {
		t.Errorf("state = %q, want %q", last.State, monitor.StateDown)
	}
	if last.SuccessRatio != 0 {
		t.Errorf("success_ratio = %v, want 0", last.SuccessRatio)
	}
	if last.AvgDurationMS != 2000 {
		t.Errorf("avg_duration_ms = %d, want 2000", last.AvgDurationMS)
	}
}

// TestQueryHistory_FiltersByMonitorID verifies samples for other monitors do
// not leak into the requested monitor's history.
func TestQueryHistory_FiltersByMonitorID(t *testing.T) {
	store := openStore(t)
	now := time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC)

	if err := store.WriteCheck(context.Background(), CheckSample{
		MonitorID: "mon-a", MonitorType: "http",
		FinishedAt: now.Add(-15 * time.Second), Success: true, Duration: 100 * time.Millisecond,
	}); err != nil {
		t.Fatalf("WriteCheck a: %v", err)
	}
	if err := store.WriteCheck(context.Background(), CheckSample{
		MonitorID: "mon-b", MonitorType: "http",
		FinishedAt: now.Add(-15 * time.Second), Success: false, Duration: 1 * time.Second,
	}); err != nil {
		t.Fatalf("WriteCheck b: %v", err)
	}

	points, err := store.QueryHistory(context.Background(), HistoryQuery{
		MonitorID: "mon-a", Range: Range1h, Now: now,
	})
	if err != nil {
		t.Fatalf("QueryHistory: %v", err)
	}
	last := points[len(points)-1]
	if last.State != monitor.StateUp || last.SuccessRatio != 1.0 || last.AvgDurationMS != 100 {
		t.Errorf("mon-a last bucket = %+v, want state=up ratio=1 dur=100", last)
	}
}

// TestQueryHistory_UnsupportedRangeReturnsError verifies the SPEC §14.5
// supported-range allowlist is enforced.
func TestQueryHistory_UnsupportedRangeReturnsError(t *testing.T) {
	store := openStore(t)
	_, err := store.QueryHistory(context.Background(), HistoryQuery{
		MonitorID: "mon-x", Range: Range("12h"), Now: time.Now().UTC(),
	})
	if err == nil {
		t.Fatal("QueryHistory with unsupported range = nil, want error")
	}
}

// TestSupportedRangesMetadata verifies the public metadata helpers used by the
// IPC handler (M8.3) to validate the range query parameter.
func TestSupportedRangesMetadata(t *testing.T) {
	want := []Range{Range1h, Range6h, Range24h, Range7d, Range30d}
	got := SupportedRanges()
	if len(got) != len(want) {
		t.Fatalf("SupportedRanges len = %d, want %d", len(got), len(want))
	}
	for i, r := range want {
		if got[i] != r {
			t.Errorf("SupportedRanges[%d] = %q, want %q", i, got[i], r)
		}
	}

	resCases := map[Range]time.Duration{
		Range1h: 1 * time.Minute, Range6h: 5 * time.Minute, Range24h: 15 * time.Minute,
		Range7d: 1 * time.Hour, Range30d: 6 * time.Hour,
	}
	for r, want := range resCases {
		got, ok := ResolutionFor(r)
		if !ok || got != want {
			t.Errorf("ResolutionFor(%q) = (%v, %v), want (%v, true)", r, got, ok, want)
		}
	}
	if _, ok := ResolutionFor(Range("12h")); ok {
		t.Errorf("ResolutionFor unsupported = ok, want !ok")
	}
}
