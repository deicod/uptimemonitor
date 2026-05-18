package ipc

import (
	"encoding/json"
	"testing"
	"time"
)

// ---------- StatusResponse round-trip ----------

func TestStatusResponseRoundTrip(t *testing.T) {
	started := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	original := StatusResponse{
		Version:   "0.1.0-dev",
		State:     "ready",
		StartedAt: started,
		SQLite:    StoreHealth{OK: true},
		TSDB:      StoreHealth{OK: true},
		Scheduler: SchedulerStatus{Running: true, Workers: 16},
		Monitors:  MonitorCounts{Total: 3, Active: 2},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var got StatusResponse
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if got.Version != original.Version {
		t.Errorf("Version = %q, want %q", got.Version, original.Version)
	}
	if got.State != original.State {
		t.Errorf("State = %q, want %q", got.State, original.State)
	}
	if !got.StartedAt.Equal(original.StartedAt) {
		t.Errorf("StartedAt = %v, want %v", got.StartedAt, original.StartedAt)
	}
	if got.SQLite.OK != original.SQLite.OK {
		t.Errorf("SQLite.OK = %v, want %v", got.SQLite.OK, original.SQLite.OK)
	}
	if got.TSDB.OK != original.TSDB.OK {
		t.Errorf("TSDB.OK = %v, want %v", got.TSDB.OK, original.TSDB.OK)
	}
	if got.Scheduler.Running != original.Scheduler.Running {
		t.Errorf("Scheduler.Running = %v, want %v", got.Scheduler.Running, original.Scheduler.Running)
	}
	if got.Scheduler.Workers != original.Scheduler.Workers {
		t.Errorf("Scheduler.Workers = %d, want %d", got.Scheduler.Workers, original.Scheduler.Workers)
	}
	if got.Monitors.Total != original.Monitors.Total {
		t.Errorf("Monitors.Total = %d, want %d", got.Monitors.Total, original.Monitors.Total)
	}
	if got.Monitors.Active != original.Monitors.Active {
		t.Errorf("Monitors.Active = %d, want %d", got.Monitors.Active, original.Monitors.Active)
	}
}

// ---------- StatusResponse JSON field names match SPEC ----------

func TestStatusResponseJSONFieldNames(t *testing.T) {
	resp := StatusResponse{
		Version:   "v",
		State:     "s",
		StartedAt: time.Now().UTC(),
		SQLite:    StoreHealth{OK: true},
		TSDB:      StoreHealth{OK: false},
		Scheduler: SchedulerStatus{Running: true, Workers: 4},
		Monitors:  MonitorCounts{Total: 1, Active: 1},
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal raw: %v", err)
	}

	required := []string{"version", "state", "started_at", "sqlite", "tsdb", "scheduler", "monitors"}
	for _, key := range required {
		if _, ok := raw[key]; !ok {
			t.Errorf("missing JSON key %q", key)
		}
	}
}

// ---------- StoreHealth JSON shape ----------

func TestStoreHealthJSON(t *testing.T) {
	h := StoreHealth{OK: true}
	data, _ := json.Marshal(h)
	var raw map[string]any
	json.Unmarshal(data, &raw)

	if _, ok := raw["ok"]; !ok {
		t.Error("StoreHealth missing JSON key \"ok\"")
	}
}

// ---------- SchedulerStatus JSON shape ----------

func TestSchedulerStatusJSON(t *testing.T) {
	s := SchedulerStatus{Running: true, Workers: 8}
	data, _ := json.Marshal(s)
	var raw map[string]any
	json.Unmarshal(data, &raw)

	for _, key := range []string{"running", "workers"} {
		if _, ok := raw[key]; !ok {
			t.Errorf("SchedulerStatus missing JSON key %q", key)
		}
	}
}

// ---------- MonitorCounts JSON shape ----------

func TestMonitorCountsJSON(t *testing.T) {
	m := MonitorCounts{Total: 5, Active: 3}
	data, _ := json.Marshal(m)
	var raw map[string]any
	json.Unmarshal(data, &raw)

	for _, key := range []string{"total", "active"} {
		if _, ok := raw[key]; !ok {
			t.Errorf("MonitorCounts missing JSON key %q", key)
		}
	}
}

// ---------- StatusResponse zero value is valid JSON ----------

func TestStatusResponseZeroValue(t *testing.T) {
	var resp StatusResponse
	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Marshal zero value: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("Marshal zero value produced empty output")
	}
}
