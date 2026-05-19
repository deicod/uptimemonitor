package ipc

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

// fakeStatusProvider is a StatusProvider that returns a fixed snapshot.
type fakeStatusProvider struct {
	resp StatusResponse
}

func (f fakeStatusProvider) Status(context.Context) StatusResponse { return f.resp }

func sampleStatus() StatusResponse {
	return StatusResponse{
		Version:   "0.1.0-dev",
		State:     "ready",
		StartedAt: time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC),
		SQLite:    StoreHealth{OK: true},
		TSDB:      StoreHealth{OK: true},
		Scheduler: SchedulerStatus{Running: true, Workers: 16},
		Monitors:  MonitorCounts{Total: 3, Active: 2},
	}
}

// ---------- StatusHandler returns the expected JSON ----------

func TestStatusHandler(t *testing.T) {
	want := sampleStatus()
	h := StatusHandler(fakeStatusProvider{resp: want})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/status", nil)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var got StatusResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode body: %v (body=%q)", err, rec.Body.String())
	}

	if got.Version != want.Version {
		t.Errorf("Version = %q, want %q", got.Version, want.Version)
	}
	if got.State != want.State {
		t.Errorf("State = %q, want %q", got.State, want.State)
	}
	if !got.StartedAt.Equal(want.StartedAt) {
		t.Errorf("StartedAt = %v, want %v", got.StartedAt, want.StartedAt)
	}
	if got.SQLite != want.SQLite || got.TSDB != want.TSDB {
		t.Errorf("store health = %+v/%+v, want %+v/%+v", got.SQLite, got.TSDB, want.SQLite, want.TSDB)
	}
	if got.Scheduler != want.Scheduler {
		t.Errorf("Scheduler = %+v, want %+v", got.Scheduler, want.Scheduler)
	}
	if got.Monitors != want.Monitors {
		t.Errorf("Monitors = %+v, want %+v", got.Monitors, want.Monitors)
	}
}

// ---------- NewRouter wires GET /v1/status ----------

func TestNewRouterStatusRoute(t *testing.T) {
	want := sampleStatus()
	mux := NewRouter(fakeStatusProvider{resp: want})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/status", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var got StatusResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode body: %v (body=%q)", err, rec.Body.String())
	}
	if got.Version != want.Version {
		t.Errorf("Version = %q, want %q", got.Version, want.Version)
	}
}

// ---------- Client.Status decodes the endpoint response ----------

func TestClientStatus(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "test.sock")
	want := sampleStatus()

	srv := startTestServer(t, sock, NewRouter(fakeStatusProvider{resp: want}))
	defer srv.cancel()

	client := NewClient(sock)

	got, err := client.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}

	if got.Version != want.Version {
		t.Errorf("Version = %q, want %q", got.Version, want.Version)
	}
	if !got.StartedAt.Equal(want.StartedAt) {
		t.Errorf("StartedAt = %v, want %v", got.StartedAt, want.StartedAt)
	}
	if got.Scheduler != want.Scheduler {
		t.Errorf("Scheduler = %+v, want %+v", got.Scheduler, want.Scheduler)
	}
	if got.Monitors != want.Monitors {
		t.Errorf("Monitors = %+v, want %+v", got.Monitors, want.Monitors)
	}
}
