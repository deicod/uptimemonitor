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
	return mux
}

// RouterOption customises NewRouter without expanding its positional argument
// list every time a new endpoint group is added (M7.7 onwards).
type RouterOption func(*routerConfig)

type routerConfig struct {
	checker ManualChecker
	checks  CheckResultReader
	history HistoryReader
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
