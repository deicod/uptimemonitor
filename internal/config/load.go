package config

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

// envPrefix is the environment-variable prefix for all configuration keys
// (SPEC §8.4): e.g. UPTIMEMONITOR_SOCKET_PATH for socket_path.
const envPrefix = "UPTIMEMONITOR"

// configDir is the directory searched for config.yaml when --config is not
// given (SPEC §8.1).
const configDir = "/etc/uptimemonitor"

// defaults holds the SPEC §8.3 default for every config key. Registering all
// keys also lets Viper's AutomaticEnv resolve env-only nested values, which it
// otherwise misses for keys it has never seen.
var defaults = map[string]any{
	"data_dir":    "/var/lib/uptimemonitor",
	"runtime_dir": "/run/uptimemonitor",
	"sqlite_path": "/var/lib/uptimemonitor/config.db",
	"tsdb_path":   "/var/lib/uptimemonitor/tsdb",
	"socket_path": "/run/uptimemonitor/uptimemonitor.sock",
	"log_level":   "info",
	"secret":      "",

	"service.check_workers":    16,
	"service.default_interval": "60s",
	"service.timeout":          "10s",
	"service.shutdown_timeout": "10s",

	"retention.raw_samples":        "30d",
	"retention.aggregated_history": "365d",

	"notifications.enabled":             true,
	"notifications.max_attempts":        3,
	"notifications.initial_retry_delay": "5s",
	"notifications.max_retry_delay":     "60s",
}

// flagBindings maps command flag names to their config key. Only --config is
// excluded: it selects the config file rather than carrying a config value.
var flagBindings = map[string]string{
	"log-level":   "log_level",
	"socket-path": "socket_path",
	"data-dir":    "data_dir",
}

// Load assembles a Config from, in increasing order of precedence: the SPEC
// §8.3 defaults, a config file, environment variables (UPTIMEMONITOR_ prefix),
// and bound command flags.
//
// The config file is either the explicit --config path or, absent that,
// /etc/uptimemonitor/config.yaml; a missing discovered file is not an error,
// but a missing explicit --config file is. flags may be nil, in which case no
// flag values or --config path are consulted.
func Load(flags *pflag.FlagSet) (*Config, error) {
	v := viper.New()

	for key, val := range defaults {
		v.SetDefault(key, val)
	}

	v.SetEnvPrefix(envPrefix)
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	var configFile string
	if flags != nil {
		if f := flags.Lookup("config"); f != nil {
			configFile = f.Value.String()
		}
		for flagName, key := range flagBindings {
			f := flags.Lookup(flagName)
			if f == nil {
				continue
			}
			if err := v.BindPFlag(key, f); err != nil {
				return nil, fmt.Errorf("bind flag --%s: %w", flagName, err)
			}
		}
	}

	if configFile != "" {
		v.SetConfigFile(configFile)
	} else {
		v.SetConfigName("config")
		v.SetConfigType("yaml")
		v.AddConfigPath(configDir)
	}

	if err := v.ReadInConfig(); err != nil {
		// A discovered file that simply does not exist is fine — defaults and
		// env still apply. An explicitly requested file that fails to load is
		// a hard error.
		var notFound viper.ConfigFileNotFoundError
		if configFile != "" || !errors.As(err, &notFound) {
			return nil, fmt.Errorf("read config: %w", err)
		}
	}

	var cfg Config
	if err := v.Unmarshal(&cfg, viper.DecodeHook(StringToDurationHookFunc())); err != nil {
		return nil, fmt.Errorf("decode config: %w", err)
	}
	return &cfg, nil
}
