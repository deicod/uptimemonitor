package tsdb

import (
	"context"
	"fmt"
	"time"

	"github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/model/labels"
)

// Metric names emitted per check (SPEC §14.2).
const (
	MetricProbeSuccess  = "uptimemonitor_probe_success"
	MetricProbeDuration = "uptimemonitor_probe_duration_seconds"
	MetricProbeStatus   = "uptimemonitor_probe_http_status_code"
)

// Label names attached to every probe series (SPEC §14.2). High-cardinality
// data (URL, error string, monitor name) lives in SQLite, not here.
const (
	LabelMonitorID   = "monitor_id"
	LabelMonitorType = "monitor_type"
)

// CheckSample is the slice of one probe execution that goes into the TSDB. It
// is intentionally narrow: only fields that map to a numeric time-series
// belong here. Sanitised error strings and other context live in SQLite.
type CheckSample struct {
	MonitorID      string
	MonitorType    string
	FinishedAt     time.Time
	Success        bool
	Duration       time.Duration
	HTTPStatusCode *int
}

// WriteCheck appends the per-check samples described by SPEC §14.3:
// uptimemonitor_probe_success (0|1), uptimemonitor_probe_duration_seconds, and
// uptimemonitor_probe_http_status_code. The status sample is omitted when
// HTTPStatusCode is nil — SPEC §14.3 prefers omission over writing 0 because
// 0 is not a valid HTTP status code.
//
// Samples are committed atomically: on any append failure the appender is
// rolled back and the error returned so the caller can decide how to surface
// it (the check pipeline logs and continues so SQLite persistence still wins).
func (s *Store) WriteCheck(ctx context.Context, c CheckSample) error {
	ts := c.FinishedAt.UnixMilli()
	app := s.Appender(ctx)

	successValue := 0.0
	if c.Success {
		successValue = 1.0
	}
	if _, err := app.Append(0, seriesLabels(MetricProbeSuccess, c), ts, successValue); err != nil {
		_ = app.Rollback()
		return fmt.Errorf("tsdb: append %s: %w", MetricProbeSuccess, err)
	}
	if _, err := app.Append(0, seriesLabels(MetricProbeDuration, c), ts, c.Duration.Seconds()); err != nil {
		_ = app.Rollback()
		return fmt.Errorf("tsdb: append %s: %w", MetricProbeDuration, err)
	}
	if c.HTTPStatusCode != nil {
		if _, err := app.Append(0, seriesLabels(MetricProbeStatus, c), ts, float64(*c.HTTPStatusCode)); err != nil {
			_ = app.Rollback()
			return fmt.Errorf("tsdb: append %s: %w", MetricProbeStatus, err)
		}
	}

	if err := app.Commit(); err != nil {
		return fmt.Errorf("tsdb: commit check samples: %w", err)
	}
	return nil
}

// seriesLabels builds the label set for one of the three probe metrics. The
// metric name is encoded as the standard __name__ label so Prometheus query
// matchers (labels.MetricName) find it.
func seriesLabels(metric string, c CheckSample) labels.Labels {
	return labels.FromStrings(
		model.MetricNameLabel, metric,
		LabelMonitorID, c.MonitorID,
		LabelMonitorType, c.MonitorType,
	)
}
