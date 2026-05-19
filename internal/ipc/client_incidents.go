package ipc

import (
	"context"
	"net/http"
	"net/url"
)

// ListIncidents fetches incidents across all monitors from GET /v1/incidents
// (SPEC §10.5).
func (c *Client) ListIncidents(ctx context.Context) ([]IncidentResponse, error) {
	var resp IncidentListResponse
	if err := c.Do(ctx, http.MethodGet, "/v1/incidents", nil, &resp); err != nil {
		return nil, err
	}
	return resp.Incidents, nil
}

// ListMonitorIncidents fetches one monitor's incidents from
// GET /v1/monitors/{id}/incidents (SPEC §10.5).
func (c *Client) ListMonitorIncidents(ctx context.Context, monitorID string) ([]IncidentResponse, error) {
	var resp IncidentListResponse
	path := "/v1/monitors/" + url.PathEscape(monitorID) + "/incidents"
	if err := c.Do(ctx, http.MethodGet, path, nil, &resp); err != nil {
		return nil, err
	}
	return resp.Incidents, nil
}

// ListEvents fetches audit-log events across all monitors from GET /v1/events
// (SPEC §10.5).
func (c *Client) ListEvents(ctx context.Context) ([]EventResponse, error) {
	var resp EventListResponse
	if err := c.Do(ctx, http.MethodGet, "/v1/events", nil, &resp); err != nil {
		return nil, err
	}
	return resp.Events, nil
}

// ListMonitorEvents fetches one monitor's audit-log events from
// GET /v1/monitors/{id}/events (SPEC §10.5).
func (c *Client) ListMonitorEvents(ctx context.Context, monitorID string) ([]EventResponse, error) {
	var resp EventListResponse
	path := "/v1/monitors/" + url.PathEscape(monitorID) + "/events"
	if err := c.Do(ctx, http.MethodGet, path, nil, &resp); err != nil {
		return nil, err
	}
	return resp.Events, nil
}
