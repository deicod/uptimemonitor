// Package logging owns the uptimemonitor service's structured logging setup
// (SPEC §23). It builds a log/slog logger from the configured log level and
// provides a small component helper.
//
// Secrets must never be logged. When attaching contextual fields, log
// identifiers (monitor_id, target_id, provider_kind) and summarized errors —
// never raw notification provider config or the service secret (SPEC §23).
package logging

import (
	"io"
	"log/slog"
	"strings"
)

// componentKey is the structured field naming the subsystem that emitted a
// record (SPEC §23 recommended fields).
const componentKey = "component"

// ParseLevel maps a configured log-level string to a slog.Level. The match is
// case-insensitive and surrounding whitespace is ignored. The bool result
// reports whether the input named a known level; for an unknown level the
// returned slog.Level is slog.LevelInfo.
func ParseLevel(level string) (slog.Level, bool) {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		return slog.LevelDebug, true
	case "info":
		return slog.LevelInfo, true
	case "warn", "warning":
		return slog.LevelWarn, true
	case "error":
		return slog.LevelError, true
	default:
		return slog.LevelInfo, false
	}
}

// New builds a structured JSON logger writing to w at the given log level. An
// unrecognized level falls back to info; callers that need to reject unknown
// levels should validate the config before calling New.
func New(level string, w io.Writer) *slog.Logger {
	lvl, _ := ParseLevel(level)
	handler := slog.NewJSONHandler(w, &slog.HandlerOptions{Level: lvl})
	return slog.New(handler)
}

// Component returns a logger derived from base with the component field set, so
// every record it emits is attributed to the named subsystem (SPEC §23).
func Component(base *slog.Logger, name string) *slog.Logger {
	return base.With(componentKey, name)
}
