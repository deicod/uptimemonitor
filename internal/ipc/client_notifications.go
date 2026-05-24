package ipc

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
)

// NotificationProviders fetches the available notification providers from
// GET /v1/notifications/providers (SPEC §10.5, §18.3). The TUI provider form
// (M9.12) consumes the returned field metadata to render its inputs.
func (c *Client) NotificationProviders(ctx context.Context) ([]NotificationProviderResponse, error) {
	var resp NotificationProvidersResponse
	if err := c.Do(ctx, http.MethodGet, "/v1/notifications/providers", nil, &resp); err != nil {
		return nil, err
	}
	return resp.Providers, nil
}

// ListNotificationTargets fetches the configured targets from
// GET /v1/notifications/targets (SPEC §10.5). Secret fields are redacted by the
// service.
func (c *Client) ListNotificationTargets(ctx context.Context) ([]NotificationTargetResponse, error) {
	var resp NotificationTargetListResponse
	if err := c.Do(ctx, http.MethodGet, "/v1/notifications/targets", nil, &resp); err != nil {
		return nil, err
	}
	return resp.Targets, nil
}

// CreateNotificationTarget creates a target via POST /v1/notifications/targets
// (SPEC §10.5). The response carries the stored, secret-redacted config.
func (c *Client) CreateNotificationTarget(ctx context.Context, req CreateNotificationTargetRequest) (NotificationTargetResponse, error) {
	var resp NotificationTargetResponse
	if err := c.Do(ctx, http.MethodPost, "/v1/notifications/targets", req, &resp); err != nil {
		return NotificationTargetResponse{}, err
	}
	return resp, nil
}

// GetNotificationTarget fetches a single target via
// GET /v1/notifications/targets/{id} (SPEC §10.5).
func (c *Client) GetNotificationTarget(ctx context.Context, id string) (NotificationTargetResponse, error) {
	var resp NotificationTargetResponse
	if err := c.Do(ctx, http.MethodGet, "/v1/notifications/targets/"+url.PathEscape(id), nil, &resp); err != nil {
		return NotificationTargetResponse{}, err
	}
	return resp, nil
}

// UpdateNotificationTarget applies a partial update via
// PATCH /v1/notifications/targets/{id} (SPEC §10.5). A blank secret left in the
// config is preserved by the service.
func (c *Client) UpdateNotificationTarget(ctx context.Context, id string, req UpdateNotificationTargetRequest) (NotificationTargetResponse, error) {
	var resp NotificationTargetResponse
	if err := c.Do(ctx, http.MethodPatch, "/v1/notifications/targets/"+url.PathEscape(id), req, &resp); err != nil {
		return NotificationTargetResponse{}, err
	}
	return resp, nil
}

// DeleteNotificationTarget soft-deletes a target via
// DELETE /v1/notifications/targets/{id} (SPEC §10.5). A successful delete has no
// response body.
func (c *Client) DeleteNotificationTarget(ctx context.Context, id string) error {
	return c.Do(ctx, http.MethodDelete, "/v1/notifications/targets/"+url.PathEscape(id), nil, nil)
}

// TestNotificationTarget sends a test notification via
// POST /v1/notifications/targets/{id}/test (SPEC §10.5). A delivery failure is
// returned as a provider_error *APIError.
func (c *Client) TestNotificationTarget(ctx context.Context, id string) (TestNotificationResponse, error) {
	var resp TestNotificationResponse
	if err := c.Do(ctx, http.MethodPost, "/v1/notifications/targets/"+url.PathEscape(id)+"/test", nil, &resp); err != nil {
		return TestNotificationResponse{}, err
	}
	return resp, nil
}

// ListNotificationAttempts fetches recent delivery attempts across all targets
// from GET /v1/notifications/attempts (SPEC §10.5). A non-positive limit omits
// the query parameter and the server applies its default.
func (c *Client) ListNotificationAttempts(ctx context.Context, limit int) ([]NotificationAttemptResponse, error) {
	path := "/v1/notifications/attempts"
	if limit > 0 {
		path += "?limit=" + strconv.Itoa(limit)
	}
	var resp NotificationAttemptListResponse
	if err := c.Do(ctx, http.MethodGet, path, nil, &resp); err != nil {
		return nil, err
	}
	return resp.Attempts, nil
}

// GetNotificationsEnabled reads the global notifications toggle from
// GET /v1/notifications/settings (SPEC §18.6).
func (c *Client) GetNotificationsEnabled(ctx context.Context) (bool, error) {
	var resp NotificationSettingsResponse
	if err := c.Do(ctx, http.MethodGet, "/v1/notifications/settings", nil, &resp); err != nil {
		return false, err
	}
	return resp.Enabled, nil
}

// SetNotificationsEnabled flips the global notifications toggle via
// PUT /v1/notifications/settings and returns the resulting value (SPEC §18.6).
func (c *Client) SetNotificationsEnabled(ctx context.Context, enabled bool) (bool, error) {
	var resp NotificationSettingsResponse
	req := UpdateNotificationSettingsRequest{Enabled: enabled}
	if err := c.Do(ctx, http.MethodPut, "/v1/notifications/settings", req, &resp); err != nil {
		return false, err
	}
	return resp.Enabled, nil
}
