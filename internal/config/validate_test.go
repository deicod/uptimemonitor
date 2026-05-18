package config

import (
	"errors"
	"testing"
	"time"
)

// validConfig returns a baseline Config that passes every SPEC §8.5 rule. Each
// validation test mutates exactly one field so a failure pins the rule.
func validConfig() Config {
	return Config{
		DataDir:    "/var/lib/uptimemonitor",
		RuntimeDir: "/run/uptimemonitor",
		SQLitePath: "/var/lib/uptimemonitor/config.db",
		TSDBPath:   "/var/lib/uptimemonitor/tsdb",
		SocketPath: "/run/uptimemonitor/uptimemonitor.sock",
		LogLevel:   "info",
		Service: ServiceConfig{
			CheckWorkers:    16,
			DefaultInterval: 60 * time.Second,
			Timeout:         10 * time.Second,
			ShutdownTimeout: 10 * time.Second,
		},
		Retention: RetentionConfig{
			RawSamples:        30 * 24 * time.Hour,
			AggregatedHistory: 365 * 24 * time.Hour,
		},
		Notifications: NotificationConfig{
			Enabled:           true,
			MaxAttempts:       3,
			InitialRetryDelay: 5 * time.Second,
			MaxRetryDelay:     60 * time.Second,
		},
	}
}

// TestValidate exercises one failure case per SPEC §8.5 rule plus the valid
// baseline. The rules exist to fail fast: a bad config should be rejected at
// startup, with an error naming the field, rather than producing a service
// that misbehaves later (e.g. a timeout that swallows the whole interval).
func TestValidate(t *testing.T) {
	cases := []struct {
		name      string
		mutate    func(*Config)
		wantField string // "" means the config must be accepted
	}{
		{name: "valid baseline", mutate: func(*Config) {}},
		{name: "empty data_dir", mutate: func(c *Config) { c.DataDir = "" }, wantField: "data_dir"},
		{name: "empty runtime_dir", mutate: func(c *Config) { c.RuntimeDir = "" }, wantField: "runtime_dir"},
		{name: "empty sqlite_path", mutate: func(c *Config) { c.SQLitePath = "" }, wantField: "sqlite_path"},
		{name: "empty tsdb_path", mutate: func(c *Config) { c.TSDBPath = "" }, wantField: "tsdb_path"},
		{name: "empty socket_path", mutate: func(c *Config) { c.SocketPath = "" }, wantField: "socket_path"},
		{name: "zero workers", mutate: func(c *Config) { c.Service.CheckWorkers = 0 }, wantField: "service.check_workers"},
		{name: "sub-second interval", mutate: func(c *Config) { c.Service.DefaultInterval = 500 * time.Millisecond }, wantField: "service.default_interval"},
		{name: "sub-second timeout", mutate: func(c *Config) { c.Service.Timeout = 0 }, wantField: "service.timeout"},
		{name: "timeout not less than interval", mutate: func(c *Config) { c.Service.Timeout = c.Service.DefaultInterval }, wantField: "service.timeout"},
		{name: "zero max_attempts", mutate: func(c *Config) { c.Notifications.MaxAttempts = 0 }, wantField: "notifications.max_attempts"},
		{name: "sub-second initial_retry_delay", mutate: func(c *Config) { c.Notifications.InitialRetryDelay = 0 }, wantField: "notifications.initial_retry_delay"},
		{name: "max_retry_delay below initial", mutate: func(c *Config) { c.Notifications.MaxRetryDelay = time.Second }, wantField: "notifications.max_retry_delay"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := validConfig()
			c.mutate(&cfg)

			err := Validate(&cfg)
			if c.wantField == "" {
				if err != nil {
					t.Fatalf("Validate: unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Validate: expected error for field %q, got nil", c.wantField)
			}
			var fe *FieldError
			if !errors.As(err, &fe) {
				t.Fatalf("Validate: error %v is not a *FieldError", err)
			}
			if fe.Field != c.wantField {
				t.Fatalf("Validate: error field = %q, want %q", fe.Field, c.wantField)
			}
		})
	}
}
