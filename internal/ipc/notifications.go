package ipc

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/deicod/uptimemonitor/internal/monitor"
	"github.com/deicod/uptimemonitor/internal/notify"
	"github.com/deicod/uptimemonitor/internal/store/sqlite"
)

// NotificationProviderResponse is the DTO for one entry in the providers list
// (SPEC §10.5 GET /v1/notifications/providers). The TUI consumes Fields
// verbatim to render its provider form (M9.12), so the JSON shape must
// match SPEC §18.4 — that comes from notify.Field's tags directly.
type NotificationProviderResponse struct {
	Kind        string         `json:"kind"`
	DisplayName string         `json:"display_name"`
	Fields      []notify.Field `json:"fields"`
}

// NotificationProvidersResponse is the top-level DTO returned by
// GET /v1/notifications/providers.
type NotificationProvidersResponse struct {
	Providers []NotificationProviderResponse `json:"providers"`
}

// NotificationProviderRegistry is the subset of notify.Registry the IPC
// providers endpoint needs. *notify.Registry satisfies it; the interface is
// declared here so handler tests can substitute a fake without depending on
// the registry's mutation API.
type NotificationProviderRegistry interface {
	List() []notify.Provider
}

// listProvidersHandler serves GET /v1/notifications/providers (SPEC §10.5,
// §18.3). The registry's List() is the authoritative ordering — the
// handler must not re-sort, so a deterministic registry order is the only
// guarantee the wire format is stable.
func listProvidersHandler(reg NotificationProviderRegistry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		providers := reg.List()
		resp := NotificationProvidersResponse{
			Providers: make([]NotificationProviderResponse, 0, len(providers)),
		}
		for _, p := range providers {
			fields := p.Fields()
			if fields == nil {
				fields = []notify.Field{}
			}
			resp.Providers = append(resp.Providers, NotificationProviderResponse{
				Kind:        p.Kind(),
				DisplayName: p.DisplayName(),
				Fields:      fields,
			})
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

// NotificationTargetResponse is the DTO for a single notification target (SPEC
// §10.5). Config carries the provider config with secret fields already
// redacted to "" by the repository (SPEC §18.9) — the IPC layer never emits
// secret values.
type NotificationTargetResponse struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Kind      string          `json:"kind"`
	Enabled   bool            `json:"enabled"`
	Config    json.RawMessage `json:"config"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

// NotificationTargetListResponse is the DTO returned by GET /v1/notifications/targets.
type NotificationTargetListResponse struct {
	Targets []NotificationTargetResponse `json:"targets"`
}

// CreateNotificationTargetRequest is the body of POST /v1/notifications/targets
// (SPEC §10.5). Config is the provider-specific payload; its secrets are stored
// verbatim but never echoed back (the create response re-reads the redacted row).
type CreateNotificationTargetRequest struct {
	Name    string          `json:"name"`
	Kind    string          `json:"kind"`
	Enabled bool            `json:"enabled"`
	Config  json.RawMessage `json:"config"`
}

// UpdateNotificationTargetRequest is the body of PATCH /v1/notifications/targets/{id}.
// Kind is immutable — the config shape is kind-specific — so it is not updatable.
// A nil field leaves the stored value unchanged; a blank secret left inside
// Config is preserved by the repository (SPEC §18.9).
type UpdateNotificationTargetRequest struct {
	Name    *string         `json:"name"`
	Enabled *bool           `json:"enabled"`
	Config  json.RawMessage `json:"config"`
}

// NotificationAttemptResponse is the DTO for one delivery attempt (SPEC §11.6,
// §10.5). Optional foreign keys are pointers so a manual_test (which has no
// incident) renders them as null.
type NotificationAttemptResponse struct {
	ID            string     `json:"id"`
	TargetID      string     `json:"target_id"`
	MonitorID     *string    `json:"monitor_id"`
	IncidentID    *string    `json:"incident_id"`
	EventID       *string    `json:"event_id"`
	EventType     string     `json:"event_type"`
	Status        string     `json:"status"`
	AttemptNumber int        `json:"attempt_number"`
	Error         string     `json:"error,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	SentAt        *time.Time `json:"sent_at"`
}

// NotificationAttemptListResponse is the DTO returned by GET /v1/notifications/attempts.
type NotificationAttemptListResponse struct {
	Attempts []NotificationAttemptResponse `json:"attempts"`
}

// TestNotificationResponse is returned by POST /v1/notifications/targets/{id}/test
// on a successful send. A failed delivery is reported as a provider_error
// envelope, not a 200 with sent=false, so callers handle it through the same
// error path as every other endpoint.
type TestNotificationResponse struct {
	Sent bool `json:"sent"`
}

// NotificationTargetStore is the subset of the target repository the IPC layer
// needs (*sqlite.NotificationTargetRepo satisfies it). Get/List return configs
// with secrets redacted; GetWithSecrets is reserved for the test endpoint,
// which must hand the provider real credentials to actually deliver (SPEC §18.9).
type NotificationTargetStore interface {
	List(ctx context.Context) ([]*notify.Target, error)
	Get(ctx context.Context, id string) (*notify.Target, error)
	GetWithSecrets(ctx context.Context, id string) (*notify.Target, error)
	Insert(ctx context.Context, t *notify.Target) error
	Update(ctx context.Context, t *notify.Target) error
	SoftDelete(ctx context.Context, id string) error
}

// NotificationAttemptReader lists recent delivery attempts across all targets
// (*sqlite.NotificationAttemptRepo satisfies it).
type NotificationAttemptReader interface {
	ListRecent(ctx context.Context, limit int) ([]*notify.Attempt, error)
}

// NotificationTester delivers a one-off manual test to a single target
// (*notify.Pipeline satisfies it). It never retries (SPEC §18.7).
type NotificationTester interface {
	Test(ctx context.Context, target *notify.Target, msg notify.Message) error
}

// listTargetsHandler serves GET /v1/notifications/targets (SPEC §10.5). The
// repository redacts secrets before they reach this handler.
func listTargetsHandler(store NotificationTargetStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		targets, err := store.List(r.Context())
		if err != nil {
			writeAPIError(w, mapTargetError(err))
			return
		}
		resp := NotificationTargetListResponse{Targets: make([]NotificationTargetResponse, 0, len(targets))}
		for _, t := range targets {
			resp.Targets = append(resp.Targets, targetToResponse(t))
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

// createTargetHandler serves POST /v1/notifications/targets (SPEC §10.5). It
// stores the supplied config (secrets and all) and then re-reads the redacted
// row, so the response never echoes the secrets the caller just submitted.
func createTargetHandler(store NotificationTargetStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req CreateNotificationTargetRequest
		if apiErr := decodeJSON(r, &req); apiErr != nil {
			writeAPIError(w, apiErr)
			return
		}
		if apiErr := validateTargetName(req.Name); apiErr != nil {
			writeAPIError(w, apiErr)
			return
		}
		if req.Kind == "" {
			writeAPIError(w, NewAPIError(ErrValidation, "kind is required", "kind"))
			return
		}
		now := time.Now().UTC()
		t := &notify.Target{
			ID:        monitor.NewID(),
			Name:      req.Name,
			Kind:      req.Kind,
			Enabled:   req.Enabled,
			Config:    req.Config,
			CreatedAt: now,
			UpdatedAt: now,
		}
		if err := store.Insert(r.Context(), t); err != nil {
			writeAPIError(w, mapTargetError(err))
			return
		}
		created, err := store.Get(r.Context(), t.ID)
		if err != nil {
			writeAPIError(w, mapTargetError(err))
			return
		}
		writeJSON(w, http.StatusCreated, targetToResponse(created))
	}
}

// getTargetHandler serves GET /v1/notifications/targets/{id} (SPEC §10.5).
func getTargetHandler(store NotificationTargetStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		t, err := store.Get(r.Context(), r.PathValue("id"))
		if err != nil {
			writeAPIError(w, mapTargetError(err))
			return
		}
		writeJSON(w, http.StatusOK, targetToResponse(t))
	}
}

// updateTargetHandler serves PATCH /v1/notifications/targets/{id} (SPEC §10.5).
// It loads the redacted existing target, applies the supplied fields, then hands
// the result to the repository. When the caller omits Config, the redacted
// config is passed through unchanged: the repository restores any blanked secret
// from storage (SPEC §18.9), so a name-only update never destroys a stored
// secret. The response re-reads the redacted row.
func updateTargetHandler(store NotificationTargetStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		existing, err := store.Get(r.Context(), id)
		if err != nil {
			writeAPIError(w, mapTargetError(err))
			return
		}
		var req UpdateNotificationTargetRequest
		if apiErr := decodeJSON(r, &req); apiErr != nil {
			writeAPIError(w, apiErr)
			return
		}
		t := &notify.Target{
			ID:        id,
			Name:      existing.Name,
			Kind:      existing.Kind,
			Enabled:   existing.Enabled,
			Config:    existing.Config,
			UpdatedAt: time.Now().UTC(),
		}
		if req.Name != nil {
			t.Name = *req.Name
		}
		if req.Enabled != nil {
			t.Enabled = *req.Enabled
		}
		if len(req.Config) > 0 && string(req.Config) != "null" {
			t.Config = req.Config
		}
		if apiErr := validateTargetName(t.Name); apiErr != nil {
			writeAPIError(w, apiErr)
			return
		}
		if err := store.Update(r.Context(), t); err != nil {
			writeAPIError(w, mapTargetError(err))
			return
		}
		updated, err := store.Get(r.Context(), id)
		if err != nil {
			writeAPIError(w, mapTargetError(err))
			return
		}
		writeJSON(w, http.StatusOK, targetToResponse(updated))
	}
}

// deleteTargetHandler serves DELETE /v1/notifications/targets/{id} (SPEC §10.5).
// The repository soft-deletes the target; a successful delete has no body.
func deleteTargetHandler(store NotificationTargetStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := store.SoftDelete(r.Context(), r.PathValue("id")); err != nil {
			writeAPIError(w, mapTargetError(err))
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// testTargetHandler serves POST /v1/notifications/targets/{id}/test (SPEC §10.5,
// §18.9). It loads the target with its real config — delivery needs the
// credentials — and asks the tester to send a manual_test. The attempt is
// recorded by the pipeline regardless of outcome; a send failure is surfaced as
// provider_error.
func testTargetHandler(store NotificationTargetStore, tester NotificationTester) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		target, err := store.GetWithSecrets(r.Context(), r.PathValue("id"))
		if err != nil {
			writeAPIError(w, mapTargetError(err))
			return
		}
		// A target test has no monitor; carry the target name so the recipient
		// can tell which target the test came from.
		msg := notify.NewManualTestMessage("", target.Name, time.Now().UTC())
		if err := tester.Test(r.Context(), target, msg); err != nil {
			writeAPIError(w, NewAPIError(ErrProvider, err.Error()))
			return
		}
		writeJSON(w, http.StatusOK, TestNotificationResponse{Sent: true})
	}
}

// listAttemptsHandler serves GET /v1/notifications/attempts (SPEC §10.5).
func listAttemptsHandler(repo NotificationAttemptReader) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		limit, apiErr := parseLimit(r.URL.Query())
		if apiErr != nil {
			writeAPIError(w, apiErr)
			return
		}
		attempts, err := repo.ListRecent(r.Context(), limit)
		if err != nil {
			writeAPIError(w, mapTargetError(err))
			return
		}
		resp := NotificationAttemptListResponse{Attempts: make([]NotificationAttemptResponse, 0, len(attempts))}
		for _, a := range attempts {
			resp.Attempts = append(resp.Attempts, NotificationAttemptResponse{
				ID:            a.ID,
				TargetID:      a.TargetID,
				MonitorID:     a.MonitorID,
				IncidentID:    a.IncidentID,
				EventID:       a.EventID,
				EventType:     a.EventType,
				Status:        a.Status,
				AttemptNumber: a.AttemptNumber,
				Error:         a.Error,
				CreatedAt:     a.CreatedAt,
				SentAt:        a.SentAt,
			})
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

// NotificationSettingsResponse reports the effective global notifications
// toggle (SPEC §18.6, §6 decision 5).
type NotificationSettingsResponse struct {
	Enabled bool `json:"enabled"`
}

// UpdateNotificationSettingsRequest is the body of PUT /v1/notifications/settings.
type UpdateNotificationSettingsRequest struct {
	Enabled bool `json:"enabled"`
}

// NotificationSettingStore reads and writes the global notifications toggle.
// The service's settings-backed gate satisfies it; NotificationsEnabled returns
// the effective value (settings override, else config default).
//
// This endpoint pair is an addition beyond the SPEC §10.5 endpoint list: the
// TUI is a pure IPC client (SPEC §19.2), so the global enable toggle (SPEC
// §18.6) needs an IPC surface to read and flip the runtime setting.
type NotificationSettingStore interface {
	NotificationsEnabled(ctx context.Context) bool
	SetNotificationsEnabled(ctx context.Context, enabled bool) error
}

// getNotificationSettingsHandler serves GET /v1/notifications/settings.
func getNotificationSettingsHandler(store NotificationSettingStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, NotificationSettingsResponse{Enabled: store.NotificationsEnabled(r.Context())})
	}
}

// updateNotificationSettingsHandler serves PUT /v1/notifications/settings,
// flipping the runtime global toggle and echoing the resulting value.
func updateNotificationSettingsHandler(store NotificationSettingStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req UpdateNotificationSettingsRequest
		if apiErr := decodeJSON(r, &req); apiErr != nil {
			writeAPIError(w, apiErr)
			return
		}
		if err := store.SetNotificationsEnabled(r.Context(), req.Enabled); err != nil {
			writeAPIError(w, NewAPIError(ErrInternal, "an internal error occurred"))
			return
		}
		writeJSON(w, http.StatusOK, NotificationSettingsResponse{Enabled: store.NotificationsEnabled(r.Context())})
	}
}

// validateTargetName rejects a blank target name with a field-scoped
// validation_error.
func validateTargetName(name string) *APIError {
	if strings.TrimSpace(name) == "" {
		return NewAPIError(ErrValidation, "name is required", "name")
	}
	return nil
}

// mapTargetError translates a repository error into the matching SPEC §10.3
// code: a missing target becomes not_found; anything else internal_error.
func mapTargetError(err error) *APIError {
	if errors.Is(err, sqlite.ErrNotFound) {
		return NewAPIError(ErrNotFound, "notification target not found")
	}
	return NewAPIError(ErrInternal, "an internal error occurred")
}

// targetToResponse converts a domain target into its IPC DTO. The Config is
// passed through verbatim — the repository has already redacted its secrets.
func targetToResponse(t *notify.Target) NotificationTargetResponse {
	return NotificationTargetResponse{
		ID:        t.ID,
		Name:      t.Name,
		Kind:      t.Kind,
		Enabled:   t.Enabled,
		Config:    t.Config,
		CreatedAt: t.CreatedAt,
		UpdatedAt: t.UpdatedAt,
	}
}
