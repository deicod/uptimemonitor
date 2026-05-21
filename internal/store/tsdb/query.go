package tsdb

import (
	"context"
	"fmt"
	"time"

	"github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/tsdb/chunkenc"

	"github.com/deicod/uptimemonitor/internal/monitor"
)

// Range names a supported history window (SPEC §14.5). Ranges and their
// recommended bucket resolutions are MVP-frozen; new ranges must be added to
// the resolutions map below.
type Range string

const (
	Range1h  Range = "1h"
	Range6h  Range = "6h"
	Range24h Range = "24h"
	Range7d  Range = "7d"
	Range30d Range = "30d"
)

// rangeSpec is the duration + bucket resolution for a supported range
// (SPEC §14.5).
type rangeSpec struct {
	Duration   time.Duration
	Resolution time.Duration
}

var resolutions = map[Range]rangeSpec{
	Range1h:  {1 * time.Hour, 1 * time.Minute},
	Range6h:  {6 * time.Hour, 5 * time.Minute},
	Range24h: {24 * time.Hour, 15 * time.Minute},
	Range7d:  {7 * 24 * time.Hour, 1 * time.Hour},
	Range30d: {30 * 24 * time.Hour, 6 * time.Hour},
}

// SupportedRanges returns the MVP-supported history ranges in display order.
// The IPC handler (M8.3) uses this to validate the ?range= query parameter.
func SupportedRanges() []Range {
	return []Range{Range1h, Range6h, Range24h, Range7d, Range30d}
}

// ResolutionFor returns the bucket resolution for r. The bool is false when r
// is not in the supported set.
func ResolutionFor(r Range) (time.Duration, bool) {
	spec, ok := resolutions[r]
	if !ok {
		return 0, false
	}
	return spec.Resolution, true
}

// DurationFor returns the total time window covered by r.
func DurationFor(r Range) (time.Duration, bool) {
	spec, ok := resolutions[r]
	if !ok {
		return 0, false
	}
	return spec.Duration, true
}

// HistoryPoint is one bucket in a history query result (SPEC §10.5 history).
// An empty bucket (no samples) carries state=unknown with zero ratio and
// duration so the TUI can render a 'no data' glyph.
type HistoryPoint struct {
	Start         time.Time
	End           time.Time
	State         monitor.MonitorState
	SuccessRatio  float64
	AvgDurationMS int64
}

// HistoryQuery describes a history range query against the TSDB.
type HistoryQuery struct {
	MonitorID string
	Range     Range
	// Now anchors the query window: buckets cover [Now-Duration, Now].
	// Tests pass a deterministic Now; production callers use the current time.
	Now time.Time
}

// QueryHistory returns history buckets for the given monitor across q.Range
// ending at q.Now. The number of buckets is q.Range's total duration divided
// by its resolution (SPEC §14.5). Buckets are chronological, contiguous, and
// equal-sized.
//
// Aggregation:
//   - success_ratio = mean of probe_success samples in the bucket (0..1).
//   - avg_duration_ms = mean of probe_duration_seconds * 1000 in the bucket.
//   - state = up when ratio == 1, down when ratio < 1, unknown when no samples.
//     Any failure in a bucket downgrades it to down — partial-up isn't a state
//     this MVP models.
func (s *Store) QueryHistory(ctx context.Context, q HistoryQuery) ([]HistoryPoint, error) {
	spec, ok := resolutions[q.Range]
	if !ok {
		return nil, fmt.Errorf("tsdb: unsupported history range %q", q.Range)
	}
	if q.MonitorID == "" {
		return nil, fmt.Errorf("tsdb: monitor id is empty")
	}
	if q.Now.IsZero() {
		q.Now = time.Now().UTC()
	}

	bucketCount := int(spec.Duration / spec.Resolution)
	end := q.Now
	start := end.Add(-spec.Duration)
	resMS := spec.Resolution.Milliseconds()
	startMS := start.UnixMilli()
	endMS := end.UnixMilli()

	type acc struct {
		successSum    float64
		successCount  int
		durationSum   float64
		durationCount int
	}
	buckets := make([]acc, bucketCount)

	querier, err := s.Querier(startMS, endMS)
	if err != nil {
		return nil, fmt.Errorf("tsdb: querier: %w", err)
	}
	defer querier.Close()

	ss := querier.Select(ctx, false, nil,
		labels.MustNewMatcher(labels.MatchEqual, LabelMonitorID, q.MonitorID),
		labels.MustNewMatcher(labels.MatchRegexp, model.MetricNameLabel,
			MetricProbeSuccess+"|"+MetricProbeDuration),
	)
	for ss.Next() {
		series := ss.At()
		name := series.Labels().Get(model.MetricNameLabel)
		it := series.Iterator(nil)
		for it.Next() == chunkenc.ValFloat {
			ts, v := it.At()
			if ts < startMS || ts >= endMS {
				continue
			}
			idx := int((ts - startMS) / resMS)
			if idx < 0 || idx >= bucketCount {
				continue
			}
			switch name {
			case MetricProbeSuccess:
				buckets[idx].successSum += v
				buckets[idx].successCount++
			case MetricProbeDuration:
				buckets[idx].durationSum += v
				buckets[idx].durationCount++
			}
		}
		if err := it.Err(); err != nil {
			return nil, fmt.Errorf("tsdb: iterator: %w", err)
		}
	}
	if err := ss.Err(); err != nil {
		return nil, fmt.Errorf("tsdb: select: %w", err)
	}

	out := make([]HistoryPoint, bucketCount)
	for i := range out {
		bs := start.Add(time.Duration(i) * spec.Resolution)
		p := HistoryPoint{
			Start: bs,
			End:   bs.Add(spec.Resolution),
			State: monitor.StateUnknown,
		}
		if buckets[i].successCount > 0 {
			p.SuccessRatio = buckets[i].successSum / float64(buckets[i].successCount)
			if p.SuccessRatio >= 1.0 {
				p.State = monitor.StateUp
			} else {
				p.State = monitor.StateDown
			}
		}
		if buckets[i].durationCount > 0 {
			avgSeconds := buckets[i].durationSum / float64(buckets[i].durationCount)
			p.AvgDurationMS = int64(avgSeconds * 1000)
		}
		out[i] = p
	}
	return out, nil
}
