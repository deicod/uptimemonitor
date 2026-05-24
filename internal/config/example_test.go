package config

import (
	"testing"
	"time"

	"github.com/spf13/pflag"
)

// exampleConfigPath is config.example.yaml at the repository root, two levels
// up from this package directory.
const exampleConfigPath = "../../config.example.yaml"

// TestExampleConfigLoadsAndValidates guards the shipped config.example.yaml
// (M10.3): it must parse, decode its human durations (30d/365d/60s), and pass
// the same validation the service applies at startup. The example is the
// operator's starting point and is referenced from the README and systemd unit,
// so a stale or invalid example would send every new install down a broken path.
func TestExampleConfigLoadsAndValidates(t *testing.T) {
	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	fs.String("config", exampleConfigPath, "config file path")

	cfg, err := Load(fs)
	if err != nil {
		t.Fatalf("Load(%s): %v", exampleConfigPath, err)
	}

	if err := Validate(cfg); err != nil {
		t.Fatalf("Validate example config: %v", err)
	}

	// Spot-check fields whose decoding is non-trivial: the day-suffix duration
	// hook (SPEC §6 decision 11) and a plain duration. If these regress, the
	// example would still "load" but mean something different than documented.
	if cfg.Retention.RawSamples != 30*24*time.Hour {
		t.Errorf("RawSamples = %s, want 720h (30d)", cfg.Retention.RawSamples)
	}
	if cfg.Service.DefaultInterval != 60*time.Second {
		t.Errorf("DefaultInterval = %s, want 60s", cfg.Service.DefaultInterval)
	}
	// Validation must keep timeout below the interval (SPEC §8.5); the example
	// must model that relationship correctly.
	if cfg.Service.Timeout >= cfg.Service.DefaultInterval {
		t.Errorf("Timeout %s must be < DefaultInterval %s", cfg.Service.Timeout, cfg.Service.DefaultInterval)
	}
}
