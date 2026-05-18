package config

import (
	"fmt"
	"time"
)

// FieldError is a configuration validation failure tied to a specific field.
// Naming the field lets callers point operators at the exact key to fix.
type FieldError struct {
	Field   string
	Message string
}

func (e *FieldError) Error() string {
	return fmt.Sprintf("config: %s: %s", e.Field, e.Message)
}

// Validate checks cfg against the SPEC §8.5 fail-fast rules and returns a
// *FieldError for the first offending field, or nil if cfg is valid.
func Validate(cfg *Config) error {
	switch {
	case cfg.DataDir == "":
		return &FieldError{"data_dir", "must not be empty"}
	case cfg.RuntimeDir == "":
		return &FieldError{"runtime_dir", "must not be empty"}
	case cfg.SQLitePath == "":
		return &FieldError{"sqlite_path", "must not be empty"}
	case cfg.TSDBPath == "":
		return &FieldError{"tsdb_path", "must not be empty"}
	case cfg.SocketPath == "":
		return &FieldError{"socket_path", "must not be empty"}
	case cfg.Service.CheckWorkers < 1:
		return &FieldError{"service.check_workers", "must be at least 1"}
	case cfg.Service.DefaultInterval < time.Second:
		return &FieldError{"service.default_interval", "must be at least 1s"}
	case cfg.Service.Timeout < time.Second:
		return &FieldError{"service.timeout", "must be at least 1s"}
	case cfg.Service.Timeout >= cfg.Service.DefaultInterval:
		return &FieldError{"service.timeout", "must be less than service.default_interval"}
	case cfg.Notifications.MaxAttempts < 1:
		return &FieldError{"notifications.max_attempts", "must be at least 1"}
	case cfg.Notifications.InitialRetryDelay < time.Second:
		return &FieldError{"notifications.initial_retry_delay", "must be at least 1s"}
	case cfg.Notifications.MaxRetryDelay < cfg.Notifications.InitialRetryDelay:
		return &FieldError{"notifications.max_retry_delay", "must be at least notifications.initial_retry_delay"}
	}
	return nil
}
