// Package config defines the typed configuration for uptimemonitor and the
// helpers used to decode it. Loading (file/env/flags) lands in a later task;
// this file only owns the struct shape and the duration decode hook.
package config

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/go-viper/mapstructure/v2"
)

// Config is the top-level service configuration (SPEC §8.2).
type Config struct {
	DataDir       string             `mapstructure:"data_dir"`
	RuntimeDir    string             `mapstructure:"runtime_dir"`
	SQLitePath    string             `mapstructure:"sqlite_path"`
	TSDBPath      string             `mapstructure:"tsdb_path"`
	SocketPath    string             `mapstructure:"socket_path"`
	LogLevel      string             `mapstructure:"log_level"`
	Secret        string             `mapstructure:"secret"`
	Service       ServiceConfig      `mapstructure:"service"`
	Retention     RetentionConfig    `mapstructure:"retention"`
	Notifications NotificationConfig `mapstructure:"notifications"`
}

// ServiceConfig holds probe-scheduler tuning (SPEC §8.2).
type ServiceConfig struct {
	CheckWorkers    int           `mapstructure:"check_workers"`
	DefaultInterval time.Duration `mapstructure:"default_interval"`
	Timeout         time.Duration `mapstructure:"timeout"`
	ShutdownTimeout time.Duration `mapstructure:"shutdown_timeout"`
}

// RetentionConfig holds data-retention windows (SPEC §8.2).
type RetentionConfig struct {
	RawSamples        time.Duration `mapstructure:"raw_samples"`
	AggregatedHistory time.Duration `mapstructure:"aggregated_history"`
}

// NotificationConfig holds notification delivery and retry settings (SPEC §8.2).
type NotificationConfig struct {
	Enabled           bool          `mapstructure:"enabled"`
	MaxAttempts       int           `mapstructure:"max_attempts"`
	InitialRetryDelay time.Duration `mapstructure:"initial_retry_delay"`
	MaxRetryDelay     time.Duration `mapstructure:"max_retry_delay"`
}

// parseDuration parses a duration string, extending Go's time.ParseDuration with
// the `d` (day) and `w` (week) suffixes used by retention values such as `30d`
// and `365d` (SPEC §8.2; §6 decision 11). time.ParseDuration rejects those
// suffixes, so they are handled explicitly here.
func parseDuration(s string) (time.Duration, error) {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return 0, fmt.Errorf("empty duration")
	}

	suffix := trimmed[len(trimmed)-1]
	if suffix == 'd' || suffix == 'w' {
		n, err := strconv.ParseInt(trimmed[:len(trimmed)-1], 10, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid duration %q: %w", s, err)
		}
		unit := 24 * time.Hour
		if suffix == 'w' {
			unit = 7 * 24 * time.Hour
		}
		return time.Duration(n) * unit, nil
	}

	d, err := time.ParseDuration(trimmed)
	if err != nil {
		return 0, fmt.Errorf("invalid duration %q: %w", s, err)
	}
	return d, nil
}

// StringToDurationHookFunc returns a mapstructure decode hook that converts
// string values into time.Duration via parseDuration. It is registered with
// Viper before Unmarshal so that `d`/`w` suffixed durations decode correctly.
func StringToDurationHookFunc() mapstructure.DecodeHookFunc {
	durationType := reflect.TypeFor[time.Duration]()
	return func(from, to reflect.Type, data any) (any, error) {
		if from.Kind() != reflect.String || to != durationType {
			return data, nil
		}
		return parseDuration(data.(string))
	}
}
