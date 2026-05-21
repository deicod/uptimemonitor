package ipc

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/deicod/uptimemonitor/internal/monitor"
	"github.com/deicod/uptimemonitor/internal/store/sqlite"
	"github.com/deicod/uptimemonitor/internal/store/tsdb"
)

// fakeHistoryReader is a HistoryReader that records its inputs and returns a
// pre-configured slice of points so handler tests do not need a real TSDB.
type fakeHistoryReader struct {
	points []tsdb.HistoryPoint
	err    error

	gotQuery tsdb.HistoryQuery
}

func (f *fakeHistoryReader) QueryHistory(_ context.Context, q tsdb.HistoryQuery) ([]tsdb.HistoryPoint, error) {
	f.gotQuery = q
	return f.points, f.err
}

// TestHistoryHandler_RangeToResolution pins the SPEC §14.5 mapping: every
// supported range must be accepted and report the canonical resolution back
// to the client so the TUI can label its axis correctly.
func TestHistoryHandler_RangeToResolution(t *testing.T) {
	cases := []struct {
		rangeQS    string
		resolution string
	}{
		{"1h", "1m"},
		{"6h", "5m"},
		{"24h", "15m"},
		{"7d", "1h"},
		{"30d", "6h"},
	}
	for _, c := range cases {
		t.Run(c.rangeQS, func(t *testing.T) {
			svc := &fakeMonitorService{getResult: sampleMonitor()}
			reader := &fakeHistoryReader{}
			mux := NewRouter(fakeStatusProvider{}, svc, nil, nil, WithHistory(reader))

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/v1/monitors/01HX/history?range="+c.rangeQS, nil)
			mux.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d (body=%q)", rec.Code, http.StatusOK, rec.Body)
			}
			var got HistoryResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if got.Range != c.rangeQS {
				t.Errorf("Range = %q, want %q", got.Range, c.rangeQS)
			}
			if got.Resolution != c.resolution {
				t.Errorf("Resolution = %q, want %q", got.Resolution, c.resolution)
			}
			if got.MonitorID != "01HX" {
				t.Errorf("MonitorID = %q, want 01HX", got.MonitorID)
			}
			if reader.gotQuery.Range != tsdb.Range(c.rangeQS) {
				t.Errorf("query range = %q, want %q", reader.gotQuery.Range, c.rangeQS)
			}
			if reader.gotQuery.MonitorID != "01HX" {
				t.Errorf("query monitor id = %q, want 01HX", reader.gotQuery.MonitorID)
			}
		})
	}
}

// TestHistoryHandler_PointsEncoded verifies the wire format for one bucket so
// the TUI can rely on the shape regardless of how the TSDB layer evolves.
func TestHistoryHandler_PointsEncoded(t *testing.T) {
	start := time.Date(2026, 5, 21, 11, 55, 0, 0, time.UTC)
	end := start.Add(5 * time.Minute)
	reader := &fakeHistoryReader{points: []tsdb.HistoryPoint{{
		Start:         start,
		End:           end,
		State:         monitor.StateUp,
		SuccessRatio:  1.0,
		AvgDurationMS: 123,
	}}}
	svc := &fakeMonitorService{getResult: sampleMonitor()}
	mux := NewRouter(fakeStatusProvider{}, svc, nil, nil, WithHistory(reader))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/monitors/01HX/history?range=6h", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body=%q)", rec.Code, http.StatusOK, rec.Body)
	}
	var got HistoryResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Points) != 1 {
		t.Fatalf("points len = %d, want 1", len(got.Points))
	}
	p := got.Points[0]
	if !p.Start.Equal(start) || !p.End.Equal(end) {
		t.Errorf("times = (%v,%v), want (%v,%v)", p.Start, p.End, start, end)
	}
	if p.State != string(monitor.StateUp) {
		t.Errorf("State = %q, want up", p.State)
	}
	if p.SuccessRatio != 1.0 {
		t.Errorf("SuccessRatio = %v, want 1.0", p.SuccessRatio)
	}
	if p.AvgDurationMS != 123 {
		t.Errorf("AvgDurationMS = %d, want 123", p.AvgDurationMS)
	}
}

// TestHistoryHandler_MissingRange asserts that the range parameter is required:
// silently defaulting would obscure caller bugs and make the wire contract
// ambiguous.
func TestHistoryHandler_MissingRange(t *testing.T) {
	svc := &fakeMonitorService{getResult: sampleMonitor()}
	mux := NewRouter(fakeStatusProvider{}, svc, nil, nil, WithHistory(&fakeHistoryReader{}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/monitors/01HX/history", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d (body=%q)", rec.Code, http.StatusUnprocessableEntity, rec.Body)
	}
	apiErr, err := DecodeError(rec.Body.Bytes())
	if err != nil {
		t.Fatalf("decode error envelope: %v", err)
	}
	if apiErr.Code != ErrValidation {
		t.Errorf("code = %q, want %q", apiErr.Code, ErrValidation)
	}
	if apiErr.Field != "range" {
		t.Errorf("field = %q, want range", apiErr.Field)
	}
}

// TestHistoryHandler_UnsupportedRange asserts the supported-range allowlist
// (SPEC §14.5) — accepting arbitrary ranges would force the TSDB layer to
// invent resolutions and break the SPEC-frozen mapping.
func TestHistoryHandler_UnsupportedRange(t *testing.T) {
	svc := &fakeMonitorService{getResult: sampleMonitor()}
	mux := NewRouter(fakeStatusProvider{}, svc, nil, nil, WithHistory(&fakeHistoryReader{}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/monitors/01HX/history?range=12h", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnprocessableEntity)
	}
	apiErr, err := DecodeError(rec.Body.Bytes())
	if err != nil {
		t.Fatalf("decode error envelope: %v", err)
	}
	if apiErr.Code != ErrValidation {
		t.Errorf("code = %q, want %q", apiErr.Code, ErrValidation)
	}
	if apiErr.Field != "range" {
		t.Errorf("field = %q, want range", apiErr.Field)
	}
}

// TestHistoryHandler_NotFound asserts the monitor-existence check runs before
// the TSDB is touched so callers see a consistent 404 instead of an empty
// history for a non-existent monitor.
func TestHistoryHandler_NotFound(t *testing.T) {
	svc := &fakeMonitorService{getErr: sqlite.ErrNotFound}
	reader := &fakeHistoryReader{}
	mux := NewRouter(fakeStatusProvider{}, svc, nil, nil, WithHistory(reader))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/monitors/01HXMISSING/history?range=1h", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
	if reader.gotQuery.MonitorID != "" {
		t.Errorf("reader was called for missing monitor: %q", reader.gotQuery.MonitorID)
	}
}

// TestHistoryHandler_ReaderError covers the case where the TSDB query fails
// after the monitor check passes: the user must see a 500 rather than a
// silently empty history.
func TestHistoryHandler_ReaderError(t *testing.T) {
	svc := &fakeMonitorService{getResult: sampleMonitor()}
	reader := &fakeHistoryReader{err: errors.New("tsdb gone")}
	mux := NewRouter(fakeStatusProvider{}, svc, nil, nil, WithHistory(reader))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/monitors/01HX/history?range=1h", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}
