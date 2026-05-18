package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-viper/mapstructure/v2"
	yaml "go.yaml.in/yaml/v3"
)

// TestParseDuration covers the `d`/`w` extension to time.ParseDuration. The day
// and week suffixes matter because retention values like `30d`/`365d` (SPEC §8.2)
// would otherwise be rejected outright, silently breaking config loading.
func TestParseDuration(t *testing.T) {
	cases := []struct {
		in      string
		want    time.Duration
		wantErr bool
	}{
		{in: "60s", want: 60 * time.Second},
		{in: "10s", want: 10 * time.Second},
		{in: "30d", want: 30 * 24 * time.Hour},
		{in: "365d", want: 365 * 24 * time.Hour},
		{in: "2w", want: 14 * 24 * time.Hour},
		{in: "  5m  ", want: 5 * time.Minute},
		{in: "", wantErr: true},
		{in: "d", wantErr: true},
		{in: "30x", wantErr: true},
		{in: "abc", wantErr: true},
		{in: "1.5d", wantErr: true},
	}

	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			got, err := parseDuration(c.in)
			if c.wantErr {
				if err == nil {
					t.Fatalf("parseDuration(%q): expected error, got %v", c.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseDuration(%q): unexpected error: %v", c.in, err)
			}
			if got != c.want {
				t.Fatalf("parseDuration(%q) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

// TestStringToDurationHookFunc confirms the hook only fires for string->Duration
// conversions and leaves other field types untouched.
func TestStringToDurationHookFunc(t *testing.T) {
	var out struct {
		D time.Duration `mapstructure:"d"`
		S string        `mapstructure:"s"`
	}
	dec, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{
		DecodeHook: StringToDurationHookFunc(),
		Result:     &out,
	})
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	if err := dec.Decode(map[string]any{"d": "30d", "s": "plain"}); err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if out.D != 30*24*time.Hour {
		t.Fatalf("D = %v, want %v", out.D, 30*24*time.Hour)
	}
	if out.S != "plain" {
		t.Fatalf("S = %q, want %q", out.S, "plain")
	}

	if err := dec.Decode(map[string]any{"d": "nonsense"}); err == nil {
		t.Fatal("expected error decoding invalid duration")
	}
}

// TestConfigDecodeFromYAML decodes the SPEC §8.1 example fixture into Config to
// verify struct tags and the duration hook agree with the documented format.
func TestConfigDecodeFromYAML(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "config.yaml"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var m map[string]any
	if err := yaml.Unmarshal(raw, &m); err != nil {
		t.Fatalf("yaml unmarshal: %v", err)
	}

	var cfg Config
	dec, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{
		DecodeHook: StringToDurationHookFunc(),
		Result:     &cfg,
	})
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	if err := dec.Decode(m); err != nil {
		t.Fatalf("Decode: %v", err)
	}

	want := Config{
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
	if cfg != want {
		t.Fatalf("decoded config mismatch:\n got: %+v\nwant: %+v", cfg, want)
	}
}
