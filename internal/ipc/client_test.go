package ipc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// ---------- Client.Do success decode ----------

func TestClientDoSuccessDecode(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "test.sock")

	want := StatusResponse{
		Version:   "0.1.0-dev",
		State:     "ready",
		StartedAt: time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC),
		SQLite:    StoreHealth{OK: true},
		TSDB:      StoreHealth{OK: true},
		Scheduler: SchedulerStatus{Running: true, Workers: 16},
		Monitors:  MonitorCounts{Total: 3, Active: 2},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(want)
	})

	srv := startTestServer(t, sock, mux)
	defer srv.cancel()

	client := NewClient(sock)

	var got StatusResponse
	err := client.Do(context.Background(), http.MethodGet, "/v1/status", nil, &got)
	if err != nil {
		t.Fatalf("Do: %v", err)
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
	if got.SQLite.OK != want.SQLite.OK {
		t.Errorf("SQLite.OK = %v, want %v", got.SQLite.OK, want.SQLite.OK)
	}
	if got.Monitors.Total != want.Monitors.Total {
		t.Errorf("Monitors.Total = %d, want %d", got.Monitors.Total, want.Monitors.Total)
	}
}

// ---------- Client.Do nil result (fire-and-forget) ----------

func TestClientDoNilResult(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "test.sock")

	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/ping", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	srv := startTestServer(t, sock, mux)
	defer srv.cancel()

	client := NewClient(sock)

	// nil result should not panic or error on a 2xx with no body.
	err := client.Do(context.Background(), http.MethodPost, "/v1/ping", nil, nil)
	if err != nil {
		t.Fatalf("Do with nil result: %v", err)
	}
}

// ---------- Client.Do with request body ----------

func TestClientDoWithRequestBody(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "test.sock")

	type echoRequest struct {
		Name string `json:"name"`
	}
	type echoResponse struct {
		Received string `json:"received"`
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/echo", func(w http.ResponseWriter, r *http.Request) {
		var req echoRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(echoResponse{Received: req.Name})
	})

	srv := startTestServer(t, sock, mux)
	defer srv.cancel()

	client := NewClient(sock)

	var got echoResponse
	err := client.Do(context.Background(), http.MethodPost, "/v1/echo", echoRequest{Name: "test"}, &got)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if got.Received != "test" {
		t.Errorf("Received = %q, want %q", got.Received, "test")
	}
}

// ---------- Client.Do error envelope → typed *APIError ----------

func TestClientDoErrorEnvelope(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "test.sock")

	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/monitors/missing", func(w http.ResponseWriter, r *http.Request) {
		apiErr := NewAPIError(ErrNotFound, "monitor not found")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		w.Write(EncodeError(apiErr))
	})

	srv := startTestServer(t, sock, mux)
	defer srv.cancel()

	client := NewClient(sock)

	var result json.RawMessage
	err := client.Do(context.Background(), http.MethodGet, "/v1/monitors/missing", nil, &result)
	if err == nil {
		t.Fatal("Do returned nil error, want *APIError")
	}

	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error is %T (%v), want *APIError", err, err)
	}
	if apiErr.Code != ErrNotFound {
		t.Errorf("Code = %q, want %q", apiErr.Code, ErrNotFound)
	}
	if apiErr.Message != "monitor not found" {
		t.Errorf("Message = %q, want %q", apiErr.Message, "monitor not found")
	}
}

// ---------- Client.Do validation error with field ----------

func TestClientDoValidationError(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "test.sock")

	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/monitors", func(w http.ResponseWriter, r *http.Request) {
		apiErr := NewAPIError(ErrValidation, "interval must be at least 1s", "interval")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		w.Write(EncodeError(apiErr))
	})

	srv := startTestServer(t, sock, mux)
	defer srv.cancel()

	client := NewClient(sock)

	err := client.Do(context.Background(), http.MethodPost, "/v1/monitors", nil, nil)
	if err == nil {
		t.Fatal("Do returned nil error, want *APIError")
	}

	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error is %T (%v), want *APIError", err, err)
	}
	if apiErr.Code != ErrValidation {
		t.Errorf("Code = %q, want %q", apiErr.Code, ErrValidation)
	}
	if apiErr.Field != "interval" {
		t.Errorf("Field = %q, want %q", apiErr.Field, "interval")
	}
}

// ---------- Missing socket → readable error ----------

func TestClientMissingSocket(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "nonexistent.sock")

	client := NewClient(sock)

	err := client.Do(context.Background(), http.MethodGet, "/v1/status", nil, nil)
	if err == nil {
		t.Fatal("Do returned nil error for missing socket")
	}

	// The error must be a *ConnectionError with a human-readable message.
	var connErr *ConnectionError
	if !errors.As(err, &connErr) {
		t.Fatalf("error is %T (%v), want *ConnectionError", err, err)
	}

	// The message should mention the socket path and be user-friendly.
	msg := connErr.Error()
	if msg == "" {
		t.Fatal("ConnectionError.Error() is empty")
	}

	// Verify the underlying cause is preserved.
	if connErr.Unwrap() == nil {
		t.Error("ConnectionError.Unwrap() = nil, want underlying error")
	}
}

// ---------- Connection refused → readable error ----------

func TestClientConnectionRefused(t *testing.T) {
	// Create a socket file that nothing is listening on by creating and
	// immediately closing a listener.
	sock := filepath.Join(t.TempDir(), "dead.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	ln.Close()

	client := NewClient(sock)

	err = client.Do(context.Background(), http.MethodGet, "/v1/status", nil, nil)
	if err == nil {
		t.Fatal("Do returned nil error for dead socket")
	}

	var connErr *ConnectionError
	if !errors.As(err, &connErr) {
		t.Fatalf("error is %T (%v), want *ConnectionError", err, err)
	}
}

// ---------- Context cancellation is respected ----------

func TestClientContextCancelled(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "test.sock")

	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/slow", func(w http.ResponseWriter, r *http.Request) {
		// Block until the request context is cancelled. We use the request
		// context (not a hard sleep) so the server can shut down quickly.
		<-r.Context().Done()
	})

	srv := startTestServer(t, sock, mux)
	defer srv.cancel()

	client := NewClient(sock)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := client.Do(ctx, http.MethodGet, "/v1/slow", nil, nil)
	if err == nil {
		t.Fatal("Do returned nil error for cancelled context")
	}
}

// ---------- Non-JSON error body falls back gracefully ----------

func TestClientNonJSONErrorBody(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "test.sock")

	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/broken", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(w, "something went wrong")
	})

	srv := startTestServer(t, sock, mux)
	defer srv.cancel()

	client := NewClient(sock)

	err := client.Do(context.Background(), http.MethodGet, "/v1/broken", nil, nil)
	if err == nil {
		t.Fatal("Do returned nil error for 500 response")
	}

	// Even with a non-JSON body, we should get a usable *APIError.
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error is %T (%v), want *APIError", err, err)
	}
	if apiErr.Code != ErrInternal {
		t.Errorf("Code = %q, want %q", apiErr.Code, ErrInternal)
	}
}

// ---------- SocketPath accessor ----------

func TestClientSocketPath(t *testing.T) {
	path := "/run/uptimemonitor/uptimemonitor.sock"
	client := NewClient(path)
	if got := client.SocketPath(); got != path {
		t.Errorf("SocketPath() = %q, want %q", got, path)
	}
}

// ---------- helpers ----------

// testServer wraps a running IPC server for use in client tests.
type testServer struct {
	cancel context.CancelFunc
	errCh  chan error
}

// startTestServer starts an IPC server on the given socket with the provided
// handler. It waits until the server is accepting connections before returning.
func startTestServer(t *testing.T, sock string, handler http.Handler) *testServer {
	t.Helper()

	srv := NewServer(sock, handler)
	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Start(ctx) }()

	// Wait until the server is accepting connections.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.Dial("unix", sock)
		if err == nil {
			conn.Close()
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := os.Stat(sock); err != nil {
		cancel()
		t.Fatalf("test server socket %s did not appear: %v", sock, err)
	}

	t.Cleanup(func() {
		cancel()
		if err := <-errCh; err != nil {
			t.Errorf("test server exited with error: %v", err)
		}
	})

	return &testServer{cancel: cancel, errCh: errCh}
}
