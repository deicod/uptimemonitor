package ipc

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
)

// MonitorListFilter holds the optional filters accepted by ListMonitors,
// mirroring the state/enabled query parameters of GET /v1/monitors (SPEC
// §10.5). A zero value applies no filter.
type MonitorListFilter struct {
	// State, when non-empty, restricts the result to monitors in that state
	// (up, down, unknown, paused).
	State string
	// Enabled, when non-nil, restricts the result to enabled/disabled monitors.
	Enabled *bool
}

// query encodes the filter as URL query parameters; an empty filter yields "".
func (f MonitorListFilter) query() string {
	q := url.Values{}
	if f.State != "" {
		q.Set("state", f.State)
	}
	if f.Enabled != nil {
		q.Set("enabled", strconv.FormatBool(*f.Enabled))
	}
	if len(q) == 0 {
		return ""
	}
	return "?" + q.Encode()
}

// ListMonitors fetches monitors from GET /v1/monitors, applying the given
// filter (SPEC §10.5).
func (c *Client) ListMonitors(ctx context.Context, filter MonitorListFilter) ([]MonitorResponse, error) {
	var resp MonitorListResponse
	if err := c.Do(ctx, http.MethodGet, "/v1/monitors"+filter.query(), nil, &resp); err != nil {
		return nil, err
	}
	return resp.Monitors, nil
}

// CreateMonitor creates a monitor via POST /v1/monitors (SPEC §10.5).
func (c *Client) CreateMonitor(ctx context.Context, req CreateMonitorRequest) (MonitorResponse, error) {
	var resp MonitorResponse
	if err := c.Do(ctx, http.MethodPost, "/v1/monitors", req, &resp); err != nil {
		return MonitorResponse{}, err
	}
	return resp, nil
}

// GetMonitor fetches a single monitor via GET /v1/monitors/{id} (SPEC §10.5).
func (c *Client) GetMonitor(ctx context.Context, id string) (MonitorResponse, error) {
	var resp MonitorResponse
	if err := c.Do(ctx, http.MethodGet, "/v1/monitors/"+url.PathEscape(id), nil, &resp); err != nil {
		return MonitorResponse{}, err
	}
	return resp, nil
}

// UpdateMonitor applies a partial update via PATCH /v1/monitors/{id} (SPEC
// §10.5).
func (c *Client) UpdateMonitor(ctx context.Context, id string, req UpdateMonitorRequest) (MonitorResponse, error) {
	var resp MonitorResponse
	if err := c.Do(ctx, http.MethodPatch, "/v1/monitors/"+url.PathEscape(id), req, &resp); err != nil {
		return MonitorResponse{}, err
	}
	return resp, nil
}

// DeleteMonitor soft-deletes a monitor via DELETE /v1/monitors/{id} (SPEC
// §10.5). A successful delete has no response body.
func (c *Client) DeleteMonitor(ctx context.Context, id string) error {
	return c.Do(ctx, http.MethodDelete, "/v1/monitors/"+url.PathEscape(id), nil, nil)
}
