package logging

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"
)

func TestParseLevel(t *testing.T) {
	tests := []struct {
		in       string
		want     slog.Level
		wantKnow bool
	}{
		{"debug", slog.LevelDebug, true},
		{"info", slog.LevelInfo, true},
		{"warn", slog.LevelWarn, true},
		{"warning", slog.LevelWarn, true},
		{"error", slog.LevelError, true},
		{"  INFO ", slog.LevelInfo, true},
		{"DEBUG", slog.LevelDebug, true},
		{"verbose", slog.LevelInfo, false},
		{"", slog.LevelInfo, false},
	}
	for _, tt := range tests {
		got, known := ParseLevel(tt.in)
		if got != tt.want || known != tt.wantKnow {
			t.Errorf("ParseLevel(%q) = (%v, %v), want (%v, %v)",
				tt.in, got, known, tt.want, tt.wantKnow)
		}
	}
}

// TestNewHonorsLevel verifies the selected handler filters records below the
// configured level: at warn, a debug record must be dropped and a warn record
// kept. This matters because an over-verbose service log buries real signal.
func TestNewHonorsLevel(t *testing.T) {
	var buf bytes.Buffer
	log := New("warn", &buf)

	log.Debug("debug message")
	if buf.Len() != 0 {
		t.Fatalf("debug record emitted at warn level: %q", buf.String())
	}

	log.Warn("warn message")
	if buf.Len() == 0 {
		t.Fatal("warn record dropped at warn level")
	}
}

// TestNewEmitsStructuredJSON guards the SPEC §23 requirement that logs are
// structured (parseable), not free text.
func TestNewEmitsStructuredJSON(t *testing.T) {
	var buf bytes.Buffer
	log := New("info", &buf)
	log.Info("hello", "key", "value")

	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("log line is not JSON: %v (%q)", err, buf.String())
	}
	if rec["msg"] != "hello" || rec["key"] != "value" {
		t.Errorf("unexpected record fields: %v", rec)
	}
}

// TestNewUnknownLevelFallsBackToInfo: an invalid configured level must not
// silently disable logging; it falls back to info so records still surface.
func TestNewUnknownLevelFallsBackToInfo(t *testing.T) {
	var buf bytes.Buffer
	log := New("bogus", &buf)
	log.Info("info message")
	if buf.Len() == 0 {
		t.Fatal("info record dropped after unknown-level fallback")
	}
}

// TestComponent verifies the helper attributes records to a subsystem via the
// SPEC §23 `component` field, which downstream log filtering relies on.
func TestComponent(t *testing.T) {
	var buf bytes.Buffer
	log := Component(New("info", &buf), "scheduler")
	log.Info("started")

	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("log line is not JSON: %v", err)
	}
	if rec[componentKey] != "scheduler" {
		t.Errorf("component = %v, want %q", rec[componentKey], "scheduler")
	}
}
