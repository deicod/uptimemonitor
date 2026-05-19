package ipc

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/deicod/uptimemonitor/internal/monitor"
	"github.com/deicod/uptimemonitor/internal/store/sqlite"
)

// Duration wraps time.Duration so the IPC JSON encoding uses Go duration
// strings ("60s", "10s") as shown in SPEC §10.5, rather than the integer
// nanosecond count time.Duration marshals to by default.
type Duration time.Duration

// MarshalJSON encodes the duration as a Go duration string.
func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Duration(d).String())
}

// UnmarshalJSON decodes a Go duration string ("60s") into the duration.
func (d *Duration) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	parsed, err := time.ParseDuration(s)
	if err != nil {
		return err
	}
	*d = Duration(parsed)
	return nil
}

// MonitorResponse is the DTO returned for a single monitor (SPEC §10.5).
type MonitorResponse struct {
	ID                   string          `json:"id"`
	Name                 string          `json:"name"`
	Type                 string          `json:"type"`
	Enabled              bool            `json:"enabled"`
	Interval             Duration        `json:"interval"`
	Timeout              Duration        `json:"timeout"`
	Config               json.RawMessage `json:"config"`
	NotificationsEnabled bool            `json:"notifications_enabled"`
	CreatedAt            time.Time       `json:"created_at"`
	UpdatedAt            time.Time       `json:"updated_at"`
}

// MonitorListResponse is the DTO returned by GET /v1/monitors.
type MonitorListResponse struct {
	Monitors []MonitorResponse `json:"monitors"`
}

// CreateMonitorRequest is the body of POST /v1/monitors (SPEC §10.5).
type CreateMonitorRequest struct {
	Name                 string          `json:"name"`
	Type                 string          `json:"type"`
	Enabled              bool            `json:"enabled"`
	Interval             Duration        `json:"interval"`
	Timeout              Duration        `json:"timeout"`
	Config               json.RawMessage `json:"config"`
	NotificationsEnabled bool            `json:"notifications_enabled"`
}

// UpdateMonitorRequest is the body of PATCH /v1/monitors/{id}. Every field is
// optional: a nil field leaves the stored value unchanged (SPEC §10.5 partial
// update). The monitor type is immutable and the enabled flag changes only via
// enable/disable, so neither is part of this request.
type UpdateMonitorRequest struct {
	Name                 *string         `json:"name"`
	Interval             *Duration       `json:"interval"`
	Timeout              *Duration       `json:"timeout"`
	Config               json.RawMessage `json:"config"`
	NotificationsEnabled *bool           `json:"notifications_enabled"`
}

// MonitorService is the subset of the monitor service the IPC layer needs.
// *monitor.Service satisfies it; the interface is declared here so handler
// tests can substitute a fake and the IPC package depends only on the
// monitor domain types, not on the service's construction.
type MonitorService interface {
	List(ctx context.Context, f monitor.MonitorFilter) ([]*monitor.Monitor, error)
	Create(ctx context.Context, m *monitor.Monitor) (*monitor.Monitor, error)
	Get(ctx context.Context, id string) (*monitor.Monitor, error)
	Update(ctx context.Context, m *monitor.Monitor) (*monitor.Monitor, error)
	Delete(ctx context.Context, id string) error
}

// listMonitorsHandler serves GET /v1/monitors with optional state/enabled
// query filters (SPEC §10.5).
func listMonitorsHandler(svc MonitorService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		filter, apiErr := parseMonitorFilter(r.URL.Query())
		if apiErr != nil {
			writeAPIError(w, apiErr)
			return
		}
		monitors, err := svc.List(r.Context(), filter)
		if err != nil {
			writeAPIError(w, mapServiceError(err))
			return
		}
		resp := MonitorListResponse{Monitors: make([]MonitorResponse, 0, len(monitors))}
		for _, m := range monitors {
			resp.Monitors = append(resp.Monitors, monitorToResponse(m))
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

// createMonitorHandler serves POST /v1/monitors (SPEC §10.5).
func createMonitorHandler(svc MonitorService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req CreateMonitorRequest
		if apiErr := decodeJSON(r, &req); apiErr != nil {
			writeAPIError(w, apiErr)
			return
		}
		m := &monitor.Monitor{
			Name:                 req.Name,
			Type:                 monitor.MonitorType(req.Type),
			Enabled:              req.Enabled,
			Interval:             time.Duration(req.Interval),
			Timeout:              time.Duration(req.Timeout),
			Config:               req.Config,
			NotificationsEnabled: req.NotificationsEnabled,
		}
		created, err := svc.Create(r.Context(), m)
		if err != nil {
			writeAPIError(w, mapServiceError(err))
			return
		}
		writeJSON(w, http.StatusCreated, monitorToResponse(created))
	}
}

// getMonitorHandler serves GET /v1/monitors/{id} (SPEC §10.5).
func getMonitorHandler(svc MonitorService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		m, err := svc.Get(r.Context(), r.PathValue("id"))
		if err != nil {
			writeAPIError(w, mapServiceError(err))
			return
		}
		writeJSON(w, http.StatusOK, monitorToResponse(m))
	}
}

// updateMonitorHandler serves PATCH /v1/monitors/{id} (SPEC §10.5). It loads
// the stored monitor, applies the supplied fields, then hands the result to
// the service, which re-validates it — partial-then-validate.
func updateMonitorHandler(svc MonitorService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		existing, err := svc.Get(r.Context(), id)
		if err != nil {
			writeAPIError(w, mapServiceError(err))
			return
		}
		var req UpdateMonitorRequest
		if apiErr := decodeJSON(r, &req); apiErr != nil {
			writeAPIError(w, apiErr)
			return
		}
		if req.Name != nil {
			existing.Name = *req.Name
		}
		if req.Interval != nil {
			existing.Interval = time.Duration(*req.Interval)
		}
		if req.Timeout != nil {
			existing.Timeout = time.Duration(*req.Timeout)
		}
		if len(req.Config) > 0 && string(req.Config) != "null" {
			existing.Config = req.Config
		}
		if req.NotificationsEnabled != nil {
			existing.NotificationsEnabled = *req.NotificationsEnabled
		}
		updated, err := svc.Update(r.Context(), existing)
		if err != nil {
			writeAPIError(w, mapServiceError(err))
			return
		}
		writeJSON(w, http.StatusOK, monitorToResponse(updated))
	}
}

// deleteMonitorHandler serves DELETE /v1/monitors/{id} (SPEC §10.5). The
// service soft-deletes the monitor; a successful delete has no response body.
func deleteMonitorHandler(svc MonitorService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := svc.Delete(r.Context(), r.PathValue("id")); err != nil {
			writeAPIError(w, mapServiceError(err))
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// parseMonitorFilter builds a MonitorFilter from the state/enabled query
// parameters, rejecting unrecognised values with a bad_request error.
func parseMonitorFilter(q url.Values) (monitor.MonitorFilter, *APIError) {
	var f monitor.MonitorFilter
	if v := q.Get("state"); v != "" {
		st := monitor.MonitorState(v)
		switch st {
		case monitor.StateUp, monitor.StateDown, monitor.StateUnknown, monitor.StatePaused:
			f.State = &st
		default:
			return f, NewAPIError(ErrBadRequest, "invalid state filter value", "state")
		}
	}
	if v := q.Get("enabled"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return f, NewAPIError(ErrBadRequest, "invalid enabled filter value", "enabled")
		}
		f.Enabled = &b
	}
	return f, nil
}

// decodeJSON decodes the request body into v, returning a bad_request error if
// the body is not valid JSON.
func decodeJSON(r *http.Request, v any) *APIError {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		return NewAPIError(ErrBadRequest, "request body is not valid JSON")
	}
	return nil
}

// mapServiceError translates an error from the monitor service into the
// matching SPEC §10.3 error code. A monitor.FieldError becomes a
// validation_error carrying the field name; a missing record becomes
// not_found; anything else is reported as an internal_error.
func mapServiceError(err error) *APIError {
	var fe *monitor.FieldError
	if errors.As(err, &fe) {
		return NewAPIError(ErrValidation, fe.Message, fe.Field)
	}
	if errors.Is(err, sqlite.ErrNotFound) {
		return NewAPIError(ErrNotFound, "monitor not found")
	}
	return NewAPIError(ErrInternal, "an internal error occurred")
}

// writeAPIError writes an APIError as its JSON envelope with the canonical
// HTTP status for its code.
func writeAPIError(w http.ResponseWriter, e *APIError) {
	w.WriteHeader(e.Code.HTTPStatus())
	w.Write(EncodeError(e))
}

// monitorToResponse converts a domain monitor into its IPC DTO.
func monitorToResponse(m *monitor.Monitor) MonitorResponse {
	return MonitorResponse{
		ID:                   m.ID,
		Name:                 m.Name,
		Type:                 string(m.Type),
		Enabled:              m.Enabled,
		Interval:             Duration(m.Interval),
		Timeout:              Duration(m.Timeout),
		Config:               m.Config,
		NotificationsEnabled: m.NotificationsEnabled,
		CreatedAt:            m.CreatedAt,
		UpdatedAt:            m.UpdatedAt,
	}
}
