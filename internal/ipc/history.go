package ipc

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/deicod/uptimemonitor/internal/store/tsdb"
)

// HistoryPointResponse is the DTO for one history bucket (SPEC §10.5).
type HistoryPointResponse struct {
	Start         time.Time `json:"start"`
	End           time.Time `json:"end"`
	State         string    `json:"state"`
	SuccessRatio  float64   `json:"success_ratio"`
	AvgDurationMS int64     `json:"avg_duration_ms"`
}

// HistoryResponse is the DTO returned by GET /v1/monitors/{id}/history
// (SPEC §10.5). Resolution echoes the canonical bucket width for the range so
// the TUI can label its axis without recomputing the SPEC §14.5 table.
type HistoryResponse struct {
	MonitorID  string                 `json:"monitor_id"`
	Range      string                 `json:"range"`
	Resolution string                 `json:"resolution"`
	Points     []HistoryPointResponse `json:"points"`
}

// HistoryReader queries history buckets from the TSDB. *tsdb.Store satisfies
// it; the interface is declared here so handler tests can substitute a fake
// without spinning up a real TSDB.
type HistoryReader interface {
	QueryHistory(ctx context.Context, q tsdb.HistoryQuery) ([]tsdb.HistoryPoint, error)
}

// listMonitorHistoryHandler serves GET /v1/monitors/{id}/history?range=
// (SPEC §10.5). The monitor must exist; range= is required and must be in the
// SPEC §14.5 supported set. The resolution= query parameter is accepted for
// forward compatibility with SPEC §10.5's example but ignored for MVP — the
// server always returns the canonical resolution for the chosen range so the
// TUI cannot accidentally bucket at a width the TSDB has no samples for.
func listMonitorHistoryHandler(svc MonitorService, reader HistoryReader) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if _, err := svc.Get(r.Context(), id); err != nil {
			writeAPIError(w, mapServiceError(err))
			return
		}
		rangeStr := r.URL.Query().Get("range")
		if rangeStr == "" {
			writeAPIError(w, NewAPIError(ErrValidation, "range is required", "range"))
			return
		}
		rg := tsdb.Range(rangeStr)
		resolution, ok := tsdb.ResolutionFor(rg)
		if !ok {
			writeAPIError(w, NewAPIError(ErrValidation, "unsupported history range", "range"))
			return
		}
		points, err := reader.QueryHistory(r.Context(), tsdb.HistoryQuery{
			MonitorID: id,
			Range:     rg,
		})
		if err != nil {
			writeAPIError(w, NewAPIError(ErrInternal, "an internal error occurred"))
			return
		}
		resp := HistoryResponse{
			MonitorID:  id,
			Range:      rangeStr,
			Resolution: formatResolution(resolution),
			Points:     make([]HistoryPointResponse, 0, len(points)),
		}
		for _, p := range points {
			resp.Points = append(resp.Points, HistoryPointResponse{
				Start:         p.Start,
				End:           p.End,
				State:         string(p.State),
				SuccessRatio:  p.SuccessRatio,
				AvgDurationMS: p.AvgDurationMS,
			})
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

// formatResolution renders a canonical bucket width as a short SPEC §10.5
// string (e.g. "5m", "1h"). Go's time.Duration.String prints "5m0s" / "1h0m0s"
// for these values, which would clutter the wire format.
func formatResolution(d time.Duration) string {
	switch {
	case d%time.Hour == 0:
		return fmt.Sprintf("%dh", int(d/time.Hour))
	case d%time.Minute == 0:
		return fmt.Sprintf("%dm", int(d/time.Minute))
	default:
		return d.String()
	}
}
