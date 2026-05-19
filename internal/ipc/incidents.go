package ipc

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/deicod/uptimemonitor/internal/monitor"
)

// defaultListLimit caps the incident and event list endpoints when the caller
// does not supply an explicit ?limit=. Both tables grow unbounded between
// retention runs, so an unfiltered "all rows" default would be a footgun.
const defaultListLimit = 100

// IncidentResponse is the DTO for a single incident (SPEC §11.5, §10.5).
type IncidentResponse struct {
	ID           string     `json:"id"`
	MonitorID    string     `json:"monitor_id"`
	StartedAt    time.Time  `json:"started_at"`
	ResolvedAt   *time.Time `json:"resolved_at"`
	StartEventID string     `json:"start_event_id"`
	EndEventID   *string    `json:"end_event_id"`
	Reason       string     `json:"reason"`
}

// IncidentListResponse is the DTO returned by the incident list endpoints.
type IncidentListResponse struct {
	Incidents []IncidentResponse `json:"incidents"`
}

// EventResponse is the DTO for a single audit-log event (SPEC §11.6, §10.5).
// A nil MonitorID marks a service-scoped event.
type EventResponse struct {
	ID        string          `json:"id"`
	Type      string          `json:"type"`
	MonitorID *string         `json:"monitor_id"`
	Data      json.RawMessage `json:"data"`
	CreatedAt time.Time       `json:"created_at"`
}

// EventListResponse is the DTO returned by the event list endpoints.
type EventListResponse struct {
	Events []EventResponse `json:"events"`
}

// IncidentReader is the subset of the incident repository the IPC layer needs.
// *sqlite.IncidentRepo satisfies it; the interface is declared here so handler
// tests can substitute a fake.
type IncidentReader interface {
	// ListAll returns the most recent incidents across all monitors.
	ListAll(ctx context.Context, limit int) ([]*monitor.Incident, error)
	// List returns the most recent incidents for one monitor.
	List(ctx context.Context, monitorID string, limit int) ([]*monitor.Incident, error)
}

// EventReader is the subset of the event repository the IPC layer needs.
// *sqlite.EventRepo satisfies it; the interface is declared here so handler
// tests can substitute a fake.
type EventReader interface {
	// List returns the most recent events across all monitors.
	List(ctx context.Context, limit int) ([]*monitor.Event, error)
	// ListByMonitor returns the most recent events for one monitor.
	ListByMonitor(ctx context.Context, monitorID string, limit int) ([]*monitor.Event, error)
}

// listIncidentsHandler serves GET /v1/incidents (SPEC §10.5).
func listIncidentsHandler(repo IncidentReader) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		limit, apiErr := parseLimit(r.URL.Query())
		if apiErr != nil {
			writeAPIError(w, apiErr)
			return
		}
		incidents, err := repo.ListAll(r.Context(), limit)
		if err != nil {
			writeAPIError(w, mapServiceError(err))
			return
		}
		writeJSON(w, http.StatusOK, incidentListResponse(incidents))
	}
}

// listMonitorIncidentsHandler serves GET /v1/monitors/{id}/incidents (SPEC
// §10.5).
func listMonitorIncidentsHandler(repo IncidentReader) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		limit, apiErr := parseLimit(r.URL.Query())
		if apiErr != nil {
			writeAPIError(w, apiErr)
			return
		}
		incidents, err := repo.List(r.Context(), r.PathValue("id"), limit)
		if err != nil {
			writeAPIError(w, mapServiceError(err))
			return
		}
		writeJSON(w, http.StatusOK, incidentListResponse(incidents))
	}
}

// listEventsHandler serves GET /v1/events (SPEC §10.5).
func listEventsHandler(repo EventReader) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		limit, apiErr := parseLimit(r.URL.Query())
		if apiErr != nil {
			writeAPIError(w, apiErr)
			return
		}
		events, err := repo.List(r.Context(), limit)
		if err != nil {
			writeAPIError(w, mapServiceError(err))
			return
		}
		writeJSON(w, http.StatusOK, eventListResponse(events))
	}
}

// listMonitorEventsHandler serves GET /v1/monitors/{id}/events (SPEC §10.5).
func listMonitorEventsHandler(repo EventReader) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		limit, apiErr := parseLimit(r.URL.Query())
		if apiErr != nil {
			writeAPIError(w, apiErr)
			return
		}
		events, err := repo.ListByMonitor(r.Context(), r.PathValue("id"), limit)
		if err != nil {
			writeAPIError(w, mapServiceError(err))
			return
		}
		writeJSON(w, http.StatusOK, eventListResponse(events))
	}
}

// parseLimit reads the optional ?limit= query parameter. When absent it
// returns defaultListLimit; a non-numeric or non-positive value is rejected
// with a bad_request error.
func parseLimit(q url.Values) (int, *APIError) {
	v := q.Get("limit")
	if v == "" {
		return defaultListLimit, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		return 0, NewAPIError(ErrBadRequest, "invalid limit value", "limit")
	}
	return n, nil
}

// incidentListResponse converts domain incidents into their list DTO.
func incidentListResponse(incidents []*monitor.Incident) IncidentListResponse {
	resp := IncidentListResponse{Incidents: make([]IncidentResponse, 0, len(incidents))}
	for _, in := range incidents {
		resp.Incidents = append(resp.Incidents, IncidentResponse{
			ID:           in.ID,
			MonitorID:    in.MonitorID,
			StartedAt:    in.StartedAt,
			ResolvedAt:   in.ResolvedAt,
			StartEventID: in.StartEventID,
			EndEventID:   in.EndEventID,
			Reason:       in.Reason,
		})
	}
	return resp
}

// eventListResponse converts domain events into their list DTO.
func eventListResponse(events []*monitor.Event) EventListResponse {
	resp := EventListResponse{Events: make([]EventResponse, 0, len(events))}
	for _, e := range events {
		resp.Events = append(resp.Events, EventResponse{
			ID:        e.ID,
			Type:      e.Type,
			MonitorID: e.MonitorID,
			Data:      e.Data,
			CreatedAt: e.CreatedAt,
		})
	}
	return resp
}
