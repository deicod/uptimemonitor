package app

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"

	"github.com/deicod/uptimemonitor/internal/notify"
	"github.com/deicod/uptimemonitor/internal/notify/providers/discord"
	"github.com/deicod/uptimemonitor/internal/notify/providers/email"
	"github.com/deicod/uptimemonitor/internal/notify/providers/gotify"
	"github.com/deicod/uptimemonitor/internal/notify/providers/ntfy"
	"github.com/deicod/uptimemonitor/internal/notify/providers/slack"
	"github.com/deicod/uptimemonitor/internal/notify/providers/telegram"
	"github.com/deicod/uptimemonitor/internal/notify/providers/webhook"
	"github.com/deicod/uptimemonitor/internal/store/sqlite"
)

// settingNotificationsEnabled is the settings-table key holding the runtime
// global notifications toggle (SPEC §18.6, §6 decision 5). When set it overrides
// the static notifications.enabled config default; when absent the default
// applies, so a fresh install honours the config without a TUI round-trip.
const settingNotificationsEnabled = "notifications_enabled"

// buildNotifyRegistry constructs the provider registry with every MVP provider
// (SPEC §18.3). The fake provider is test-only and is deliberately excluded.
func buildNotifyRegistry() (*notify.Registry, error) {
	reg := notify.NewRegistry()
	for _, p := range []notify.Provider{
		webhook.New(),
		discord.New(),
		slack.New(),
		ntfy.New(),
		gotify.New(),
		telegram.New(),
		email.New(),
	} {
		if err := reg.Register(p); err != nil {
			return nil, err
		}
	}
	return reg, nil
}

// notificationGate reads the global notifications toggle from the settings
// table, falling back to the config default when the operator has never set it.
// It implements pipeline.NotificationGate. A read error falls back to the
// default rather than silently dropping notifications.
type notificationGate struct {
	settings *sqlite.SettingsRepo
	fallback bool
	logger   *slog.Logger
}

// NotificationsEnabled reports the effective global toggle.
func (g *notificationGate) NotificationsEnabled(ctx context.Context) bool {
	raw, err := g.settings.Get(ctx, settingNotificationsEnabled)
	if errors.Is(err, sqlite.ErrNotFound) {
		return g.fallback
	}
	if err != nil {
		g.logger.Error("read notifications_enabled setting", "error", err.Error())
		return g.fallback
	}
	var enabled bool
	if err := json.Unmarshal(raw, &enabled); err != nil {
		g.logger.Error("decode notifications_enabled setting", "error", err.Error())
		return g.fallback
	}
	return enabled
}

// SetNotificationsEnabled persists the runtime global toggle, overriding the
// config default until changed again. It backs PUT /v1/notifications/settings.
func (g *notificationGate) SetNotificationsEnabled(ctx context.Context, enabled bool) error {
	raw, err := json.Marshal(enabled)
	if err != nil {
		return err
	}
	return g.settings.Set(ctx, settingNotificationsEnabled, raw)
}
