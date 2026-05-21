package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/deicod/uptimemonitor/internal/notify"
)

// notificationTargetColumns is the SELECT list for a notification_targets row.
const notificationTargetColumns = `id, name, kind, enabled, config_json, ` +
	`created_at, updated_at, deleted_at`

// SecretFieldsFunc returns the names of secret fields for a provider kind.
// The repository consults it to redact secrets on read and to preserve
// stored secrets on update (SPEC §18.9). A nil func is treated as "no
// fields are secret", useful for tests and for kinds with no secrets.
type SecretFieldsFunc func(kind string) []string

// NotificationTargetRepo provides CRUD access to the notification_targets
// table (SPEC §12.3) with the secret-handling rules from SPEC §18.9: Get and
// List redact secret fields by default; Update merges blank/missing secret
// fields from the stored row so an operator can change a public field
// without re-entering every secret. Raw SQL is confined to this package per
// the SPEC §5 architecture rule.
type NotificationTargetRepo struct {
	db           *sql.DB
	secretFields SecretFieldsFunc
}

// NewNotificationTargetRepo binds a target repository to an open store.
// secretFields may be nil, in which case no fields are redacted or preserved.
func NewNotificationTargetRepo(s *Store, secretFields SecretFieldsFunc) *NotificationTargetRepo {
	return &NotificationTargetRepo{db: s.db, secretFields: secretFields}
}

// Insert persists a new notification target. The caller supplies the ID and
// timestamps. The config is stored verbatim — including any secret values —
// because the delivery pipeline needs the real secrets to authenticate.
func (r *NotificationTargetRepo) Insert(ctx context.Context, t *notify.Target) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO notification_targets (
			id, name, kind, enabled, config_json,
			created_at, updated_at, deleted_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.Name, t.Kind, boolToInt(t.Enabled),
		configJSON(t.Config),
		t.CreatedAt.UTC().Format(timeLayout),
		t.UpdatedAt.UTC().Format(timeLayout),
		nullTime(t.DeletedAt),
	)
	if err != nil {
		return fmt.Errorf("sqlite: insert notification target %s: %w", t.ID, err)
	}
	return nil
}

// Get returns the target with the given id, with secret fields redacted to
// the empty string. A soft-deleted or missing target yields ErrNotFound. IPC
// handlers should call Get; the delivery pipeline must call GetWithSecrets.
func (r *NotificationTargetRepo) Get(ctx context.Context, id string) (*notify.Target, error) {
	t, err := r.getRaw(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := r.redactConfig(t); err != nil {
		return nil, fmt.Errorf("sqlite: redact target %s: %w", id, err)
	}
	return t, nil
}

// GetWithSecrets returns the target with its full, unredacted config. It is
// reserved for the delivery pipeline; callers that surface targets to
// operators must use Get.
func (r *NotificationTargetRepo) GetWithSecrets(ctx context.Context, id string) (*notify.Target, error) {
	return r.getRaw(ctx, id)
}

// getRaw fetches a non-deleted target row without touching the config.
func (r *NotificationTargetRepo) getRaw(ctx context.Context, id string) (*notify.Target, error) {
	row := r.db.QueryRowContext(ctx,
		"SELECT "+notificationTargetColumns+" FROM notification_targets "+
			"WHERE id = ? AND deleted_at IS NULL", id,
	)
	t, err := scanNotificationTarget(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("sqlite: get notification target %s: %w", id, err)
	}
	return t, nil
}

// List returns the non-deleted targets, ordered by id (creation order), with
// secret fields redacted. The TUI target list and the /v1/notifications/targets
// IPC endpoint both build from this; an unredacted leak here would surface
// secrets across the API.
func (r *NotificationTargetRepo) List(ctx context.Context) ([]*notify.Target, error) {
	targets, err := r.listRaw(ctx)
	if err != nil {
		return nil, err
	}
	for _, t := range targets {
		if err := r.redactConfig(t); err != nil {
			return nil, fmt.Errorf("sqlite: redact target %s: %w", t.ID, err)
		}
	}
	return targets, nil
}

// ListWithSecrets returns the non-deleted targets with their full config —
// the delivery pipeline loads enabled targets through this method so it can
// actually authenticate to each provider.
func (r *NotificationTargetRepo) ListWithSecrets(ctx context.Context) ([]*notify.Target, error) {
	return r.listRaw(ctx)
}

// listRaw fetches the non-deleted target rows without touching their configs.
func (r *NotificationTargetRepo) listRaw(ctx context.Context) ([]*notify.Target, error) {
	rows, err := r.db.QueryContext(ctx,
		"SELECT "+notificationTargetColumns+" FROM notification_targets "+
			"WHERE deleted_at IS NULL ORDER BY id ASC",
	)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list notification targets: %w", err)
	}
	defer rows.Close()

	var out []*notify.Target
	for rows.Next() {
		t, err := scanNotificationTarget(rows)
		if err != nil {
			return nil, fmt.Errorf("sqlite: list notification targets: %w", err)
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: list notification targets: %w", err)
	}
	return out, nil
}

// Update persists changes to an existing target's mutable fields. If any
// secret field is missing or blank in the new config, the stored secret is
// merged back in — the TUI submits empty for fields the operator did not
// touch, and a naive overwrite would destroy the stored token (SPEC §18.9).
// A missing or soft-deleted target yields ErrNotFound.
func (r *NotificationTargetRepo) Update(ctx context.Context, t *notify.Target) error {
	merged, err := r.mergeBlankSecrets(ctx, t)
	if err != nil {
		return err
	}
	res, err := r.db.ExecContext(ctx, `
		UPDATE notification_targets SET
			name = ?, kind = ?, enabled = ?, config_json = ?, updated_at = ?
		WHERE id = ? AND deleted_at IS NULL`,
		t.Name, t.Kind, boolToInt(t.Enabled), merged,
		t.UpdatedAt.UTC().Format(timeLayout), t.ID,
	)
	if err != nil {
		return fmt.Errorf("sqlite: update notification target %s: %w", t.ID, err)
	}
	return checkAffected(res, "update notification target", t.ID)
}

// SoftDelete marks a target deleted by stamping deleted_at. The row itself
// is kept so that notification_attempts rows still resolve their target_id.
// Deleting an already-deleted or missing target yields ErrNotFound.
func (r *NotificationTargetRepo) SoftDelete(ctx context.Context, id string) error {
	now := time.Now().UTC().Format(timeLayout)
	res, err := r.db.ExecContext(ctx,
		"UPDATE notification_targets SET deleted_at = ?, updated_at = ? "+
			"WHERE id = ? AND deleted_at IS NULL",
		now, now, id,
	)
	if err != nil {
		return fmt.Errorf("sqlite: soft-delete notification target %s: %w", id, err)
	}
	return checkAffected(res, "soft-delete notification target", id)
}

// secretFieldsFor returns the secret field names for kind, or an empty slice
// if no SecretFieldsFunc was wired.
func (r *NotificationTargetRepo) secretFieldsFor(kind string) []string {
	if r.secretFields == nil {
		return nil
	}
	return r.secretFields(kind)
}

// redactConfig overwrites secret field values in t.Config with the empty
// string. Public fields are preserved unchanged, and the unmarshaled order
// of keys is not guaranteed — callers reading the config back must decode
// the JSON rather than compare bytes.
func (r *NotificationTargetRepo) redactConfig(t *notify.Target) error {
	secrets := r.secretFieldsFor(t.Kind)
	if len(secrets) == 0 || len(t.Config) == 0 {
		return nil
	}

	cfg := map[string]any{}
	if err := json.Unmarshal(t.Config, &cfg); err != nil {
		return fmt.Errorf("unmarshal config: %w", err)
	}
	for _, name := range secrets {
		if _, ok := cfg[name]; ok {
			cfg[name] = ""
		}
	}
	out, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	t.Config = out
	return nil
}

// mergeBlankSecrets returns the config to persist for an update: any secret
// field that is missing or blank in t.Config is replaced with the stored
// value. If t has no secret fields, the incoming config is returned as-is.
// A missing target yields ErrNotFound — same as the eventual UPDATE — so the
// caller sees one consistent error.
func (r *NotificationTargetRepo) mergeBlankSecrets(ctx context.Context, t *notify.Target) (string, error) {
	secrets := r.secretFieldsFor(t.Kind)
	if len(secrets) == 0 {
		return configJSON(t.Config), nil
	}

	stored, err := r.getRaw(ctx, t.ID)
	if err != nil {
		return "", err
	}

	storedCfg := map[string]any{}
	if len(stored.Config) > 0 {
		if err := json.Unmarshal(stored.Config, &storedCfg); err != nil {
			return "", fmt.Errorf("sqlite: parse stored config %s: %w", t.ID, err)
		}
	}
	newCfg := map[string]any{}
	if len(t.Config) > 0 {
		if err := json.Unmarshal(t.Config, &newCfg); err != nil {
			return "", fmt.Errorf("sqlite: parse update config %s: %w", t.ID, err)
		}
	}

	for _, name := range secrets {
		incoming, present := newCfg[name]
		if present {
			if s, ok := incoming.(string); ok && s != "" {
				continue
			}
		}
		if storedVal, ok := storedCfg[name]; ok {
			newCfg[name] = storedVal
		}
	}

	out, err := json.Marshal(newCfg)
	if err != nil {
		return "", fmt.Errorf("sqlite: marshal merged config %s: %w", t.ID, err)
	}
	return string(out), nil
}

// scanNotificationTarget reads one notification_targets row into a Target.
func scanNotificationTarget(s scannable) (*notify.Target, error) {
	var (
		t         notify.Target
		enabled   int
		config    string
		createdAt string
		updatedAt string
		deletedAt sql.NullString
	)
	if err := s.Scan(
		&t.ID, &t.Name, &t.Kind, &enabled, &config,
		&createdAt, &updatedAt, &deletedAt,
	); err != nil {
		return nil, err
	}

	t.Enabled = enabled != 0
	t.Config = []byte(config)

	var err error
	if t.CreatedAt, err = time.Parse(timeLayout, createdAt); err != nil {
		return nil, fmt.Errorf("parse created_at: %w", err)
	}
	if t.UpdatedAt, err = time.Parse(timeLayout, updatedAt); err != nil {
		return nil, fmt.Errorf("parse updated_at: %w", err)
	}
	if t.DeletedAt, err = timePtr(deletedAt); err != nil {
		return nil, fmt.Errorf("parse deleted_at: %w", err)
	}
	return &t, nil
}
