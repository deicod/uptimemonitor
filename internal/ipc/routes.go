package ipc

import "net/http"

// NewRouter assembles the /v1 route table and returns the *http.ServeMux to
// pass to NewServer.
//
// This function is the single place where the route table is assembled (SPEC
// §10.4), making it easy to see the full API surface. Each group of routes is
// registered only when its backing dependency is non-nil, so callers that
// need just a subset (e.g. status-only tests) may pass nil. Additional routes
// (notifications, etc.) are registered here as they are implemented in
// subsequent milestones.
func NewRouter(status StatusProvider, monitors MonitorService, incidents IncidentReader, events EventReader, opts ...RouterOption) *http.ServeMux {
	cfg := routerConfig{}
	for _, o := range opts {
		o(&cfg)
	}
	mux := http.NewServeMux()
	mux.Handle("GET /v1/status", StatusHandler(status))
	if monitors != nil {
		mux.Handle("GET /v1/monitors", listMonitorsHandler(monitors))
		mux.Handle("POST /v1/monitors", createMonitorHandler(monitors))
		mux.Handle("GET /v1/monitors/{id}", getMonitorHandler(monitors))
		mux.Handle("PATCH /v1/monitors/{id}", updateMonitorHandler(monitors))
		mux.Handle("DELETE /v1/monitors/{id}", deleteMonitorHandler(monitors))
		if cfg.checker != nil {
			mux.Handle("POST /v1/monitors/{id}/run", runMonitorHandler(monitors, cfg.checker))
		}
		if cfg.checks != nil {
			mux.Handle("GET /v1/monitors/{id}/checks", listMonitorChecksHandler(monitors, cfg.checks))
		}
		if cfg.history != nil {
			mux.Handle("GET /v1/monitors/{id}/history", listMonitorHistoryHandler(monitors, cfg.history))
		}
	}
	if incidents != nil {
		mux.Handle("GET /v1/incidents", listIncidentsHandler(incidents))
		mux.Handle("GET /v1/monitors/{id}/incidents", listMonitorIncidentsHandler(incidents))
	}
	if events != nil {
		mux.Handle("GET /v1/events", listEventsHandler(events))
		mux.Handle("GET /v1/monitors/{id}/events", listMonitorEventsHandler(events))
	}
	if cfg.notifications != nil {
		mux.Handle("GET /v1/notifications/providers", listProvidersHandler(cfg.notifications))
	}
	if cfg.targets != nil {
		mux.Handle("GET /v1/notifications/targets", listTargetsHandler(cfg.targets))
		mux.Handle("POST /v1/notifications/targets", createTargetHandler(cfg.targets))
		mux.Handle("GET /v1/notifications/targets/{id}", getTargetHandler(cfg.targets))
		mux.Handle("PATCH /v1/notifications/targets/{id}", updateTargetHandler(cfg.targets))
		mux.Handle("DELETE /v1/notifications/targets/{id}", deleteTargetHandler(cfg.targets))
		if cfg.tester != nil {
			mux.Handle("POST /v1/notifications/targets/{id}/test", testTargetHandler(cfg.targets, cfg.tester))
		}
	}
	if cfg.attempts != nil {
		mux.Handle("GET /v1/notifications/attempts", listAttemptsHandler(cfg.attempts))
	}
	if cfg.settings != nil {
		mux.Handle("GET /v1/notifications/settings", getNotificationSettingsHandler(cfg.settings))
		mux.Handle("PUT /v1/notifications/settings", updateNotificationSettingsHandler(cfg.settings))
	}
	return mux
}

// RouterOption customises NewRouter without expanding its positional argument
// list every time a new endpoint group is added (M7.7 onwards).
type RouterOption func(*routerConfig)

type routerConfig struct {
	checker       ManualChecker
	checks        CheckResultReader
	history       HistoryReader
	notifications NotificationProviderRegistry
	targets       NotificationTargetStore
	attempts      NotificationAttemptReader
	tester        NotificationTester
	settings      NotificationSettingStore
}

// WithManualChecker registers POST /v1/monitors/{id}/run backed by checker.
func WithManualChecker(checker ManualChecker) RouterOption {
	return func(c *routerConfig) { c.checker = checker }
}

// WithCheckResults registers GET /v1/monitors/{id}/checks backed by repo.
func WithCheckResults(repo CheckResultReader) RouterOption {
	return func(c *routerConfig) { c.checks = repo }
}

// WithHistory registers GET /v1/monitors/{id}/history backed by reader.
func WithHistory(reader HistoryReader) RouterOption {
	return func(c *routerConfig) { c.history = reader }
}

// WithNotificationRegistry registers GET /v1/notifications/providers backed
// by reg.
func WithNotificationRegistry(reg NotificationProviderRegistry) RouterOption {
	return func(c *routerConfig) { c.notifications = reg }
}

// WithNotificationTargets registers the notification-target CRUD endpoints
// (GET/POST /v1/notifications/targets and GET/PATCH/DELETE .../{id}) backed by
// store (M9.10). The test endpoint is mounted only when WithNotificationTester
// is also supplied, since that handler must first load the target.
func WithNotificationTargets(store NotificationTargetStore) RouterOption {
	return func(c *routerConfig) { c.targets = store }
}

// WithNotificationTester registers POST /v1/notifications/targets/{id}/test
// backed by tester (M9.10). It has no effect without WithNotificationTargets.
func WithNotificationTester(tester NotificationTester) RouterOption {
	return func(c *routerConfig) { c.tester = tester }
}

// WithNotificationAttempts registers GET /v1/notifications/attempts backed by
// repo (M9.10).
func WithNotificationAttempts(repo NotificationAttemptReader) RouterOption {
	return func(c *routerConfig) { c.attempts = repo }
}

// WithNotificationSettings registers GET/PUT /v1/notifications/settings backed
// by store, exposing the runtime global notifications toggle (M9.12).
func WithNotificationSettings(store NotificationSettingStore) RouterOption {
	return func(c *routerConfig) { c.settings = store }
}
