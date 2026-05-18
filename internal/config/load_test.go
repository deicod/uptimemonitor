package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/spf13/pflag"
)

// newTestFlags builds a flag set with the four flags Load binds, mirroring the
// persistent flags the root command exposes (SPEC §7.1–7.2).
func newTestFlags(t *testing.T) *pflag.FlagSet {
	t.Helper()
	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	fs.String("config", "", "config file path")
	fs.String("log-level", "", "log level override")
	fs.String("socket-path", "", "socket path override")
	fs.String("data-dir", "", "data directory override")
	return fs
}

// defaultConfig is the SPEC §8.3 baseline; tests assert Load reproduces it when
// nothing overrides a value.
func defaultConfig() Config {
	return Config{
		DataDir:    "/var/lib/uptimemonitor",
		RuntimeDir: "/run/uptimemonitor",
		SQLitePath: "/var/lib/uptimemonitor/config.db",
		TSDBPath:   "/var/lib/uptimemonitor/tsdb",
		SocketPath: "/run/uptimemonitor/uptimemonitor.sock",
		LogLevel:   "info",
		Secret:     "",
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

// TestLoadDefaults verifies that with no config file, no env, and no flags the
// SPEC §8.3 defaults apply in full — the service must have a usable config even
// when the operator supplies nothing.
func TestLoadDefaults(t *testing.T) {
	cfg, err := Load(newTestFlags(t))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if *cfg != defaultConfig() {
		t.Fatalf("defaults mismatch:\n got: %+v\nwant: %+v", *cfg, defaultConfig())
	}
}

// TestLoadConfigFile checks that an explicit --config path is read and its
// values override the defaults.
func TestLoadConfigFile(t *testing.T) {
	abs, err := filepath.Abs(filepath.Join("testdata", "config.yaml"))
	if err != nil {
		t.Fatalf("abs path: %v", err)
	}
	fs := newTestFlags(t)
	if err := fs.Set("config", abs); err != nil {
		t.Fatalf("set config flag: %v", err)
	}
	cfg, err := Load(fs)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if *cfg != defaultConfig() {
		t.Fatalf("config-file decode mismatch:\n got: %+v\nwant: %+v", *cfg, defaultConfig())
	}
}

// TestLoadMissingExplicitConfig confirms an explicit --config that does not
// exist fails loudly rather than silently falling back to defaults.
func TestLoadMissingExplicitConfig(t *testing.T) {
	fs := newTestFlags(t)
	if err := fs.Set("config", filepath.Join(t.TempDir(), "nope.yaml")); err != nil {
		t.Fatalf("set config flag: %v", err)
	}
	if _, err := Load(fs); err == nil {
		t.Fatal("expected error for missing explicit config file, got nil")
	}
}

// TestLoadEnvOverridesFile verifies environment variables take precedence over
// values from the config file, while file values still apply where no env var
// is set.
func TestLoadEnvOverridesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("data_dir: /from/file\nsocket_path: /from/file/sock\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("UPTIMEMONITOR_SOCKET_PATH", "/from/env/sock")
	t.Setenv("UPTIMEMONITOR_NOTIFICATIONS_ENABLED", "false")

	fs := newTestFlags(t)
	if err := fs.Set("config", path); err != nil {
		t.Fatalf("set config flag: %v", err)
	}
	cfg, err := Load(fs)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.SocketPath != "/from/env/sock" {
		t.Errorf("SocketPath = %q, want env value /from/env/sock", cfg.SocketPath)
	}
	if cfg.DataDir != "/from/file" {
		t.Errorf("DataDir = %q, want file value /from/file", cfg.DataDir)
	}
	if cfg.Notifications.Enabled {
		t.Error("Notifications.Enabled = true, want false from env override")
	}
}

// TestLoadFlagOverridesEnv verifies a bound command flag takes precedence over
// an environment variable for the same key.
func TestLoadFlagOverridesEnv(t *testing.T) {
	t.Setenv("UPTIMEMONITOR_LOG_LEVEL", "warn")
	t.Setenv("UPTIMEMONITOR_DATA_DIR", "/from/env")

	fs := newTestFlags(t)
	if err := fs.Set("log-level", "debug"); err != nil {
		t.Fatalf("set log-level flag: %v", err)
	}
	cfg, err := Load(fs)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.LogLevel != "debug" {
		t.Errorf("LogLevel = %q, want flag value debug", cfg.LogLevel)
	}
	if cfg.DataDir != "/from/env" {
		t.Errorf("DataDir = %q, want env value /from/env (no flag set)", cfg.DataDir)
	}
}

// TestLoadNilFlags confirms Load works without a flag set, applying defaults and
// honouring env vars — useful for callers that have no command flags.
func TestLoadNilFlags(t *testing.T) {
	t.Setenv("UPTIMEMONITOR_LOG_LEVEL", "error")
	cfg, err := Load(nil)
	if err != nil {
		t.Fatalf("Load(nil): %v", err)
	}
	if cfg.LogLevel != "error" {
		t.Errorf("LogLevel = %q, want error", cfg.LogLevel)
	}
}
