package probe_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/deicod/uptimemonitor/internal/monitor"
	"github.com/deicod/uptimemonitor/internal/probe"
)

// TestResultZeroValue documents the zero-value semantics callers rely on: a
// freshly constructed Result is a failure (Success=false), carries no error
// string, and has no HTTP status code recorded. Runners must populate the
// fields they own; nothing is implicitly truthy.
func TestResultZeroValue(t *testing.T) {
	var r probe.Result

	if r.Success {
		t.Errorf("zero Result.Success = true, want false")
	}
	if r.Error != "" {
		t.Errorf("zero Result.Error = %q, want empty", r.Error)
	}
	if r.HTTPStatusCode != nil {
		t.Errorf("zero Result.HTTPStatusCode = %v, want nil", r.HTTPStatusCode)
	}
	if !r.StartedAt.IsZero() || !r.FinishedAt.IsZero() {
		t.Errorf("zero Result timestamps not zero: started=%v finished=%v", r.StartedAt, r.FinishedAt)
	}
	if r.Duration != 0 {
		t.Errorf("zero Result.Duration = %v, want 0", r.Duration)
	}
}

// TestResultJSONRoundTrip ensures a populated Result survives JSON encoding so
// it can cross IPC and storage boundaries without losing fields — especially
// the optional HTTPStatusCode pointer, which must round-trip rather than
// silently degrade to 0.
func TestResultJSONRoundTrip(t *testing.T) {
	started := time.Date(2026, 5, 20, 10, 30, 0, 0, time.UTC)
	finished := started.Add(123 * time.Millisecond)
	status := 200

	want := probe.Result{
		StartedAt:      started,
		FinishedAt:     finished,
		Duration:       finished.Sub(started),
		Success:        true,
		Error:          "",
		HTTPStatusCode: &status,
	}

	data, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got probe.Result
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if !got.StartedAt.Equal(want.StartedAt) {
		t.Errorf("StartedAt: got %v, want %v", got.StartedAt, want.StartedAt)
	}
	if !got.FinishedAt.Equal(want.FinishedAt) {
		t.Errorf("FinishedAt: got %v, want %v", got.FinishedAt, want.FinishedAt)
	}
	if got.Duration != want.Duration {
		t.Errorf("Duration: got %v, want %v", got.Duration, want.Duration)
	}
	if got.Success != want.Success {
		t.Errorf("Success: got %v, want %v", got.Success, want.Success)
	}
	if got.Error != want.Error {
		t.Errorf("Error: got %q, want %q", got.Error, want.Error)
	}
	if got.HTTPStatusCode == nil || *got.HTTPStatusCode != *want.HTTPStatusCode {
		t.Errorf("HTTPStatusCode: got %v, want %v", got.HTTPStatusCode, want.HTTPStatusCode)
	}
}

// fakeRunner exists only to prove the Runner interface is satisfiable by a
// concrete type with the SPEC §15.1 signature. If the interface ever drifts
// from the spec this test stops compiling.
type fakeRunner struct {
	typ monitor.MonitorType
	res probe.Result
	err error
}

func (f *fakeRunner) Type() monitor.MonitorType { return f.typ }

func (f *fakeRunner) Run(_ context.Context, _ monitor.Monitor) (probe.Result, error) {
	return f.res, f.err
}

func TestRunnerInterfaceShape(t *testing.T) {
	var r probe.Runner = &fakeRunner{typ: monitor.MonitorTypeHTTP}

	if r.Type() != monitor.MonitorTypeHTTP {
		t.Errorf("Type(): got %q, want %q", r.Type(), monitor.MonitorTypeHTTP)
	}
	if _, err := r.Run(context.Background(), monitor.Monitor{}); err != nil {
		t.Errorf("Run(): unexpected error %v", err)
	}
}
