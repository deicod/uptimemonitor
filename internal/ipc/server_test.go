package ipc

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// httpClient returns an *http.Client that dials the given Unix socket.
func httpClient(socketPath string) *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("unix", socketPath)
			},
		},
	}
}

// baseURL returns the fake HTTP base URL used for requests over the Unix
// socket (the host is ignored by the transport).
func baseURL() string { return "http://uptimemonitor" }

// ---------- Server starts on a temp socket ----------

func TestServerStartsOnTempSocket(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "test.sock")
	srv := NewServer(sock, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Start(ctx) }()

	// Wait until the socket file exists.
	waitForSocket(t, sock)

	// The socket file must exist.
	info, err := os.Stat(sock)
	if err != nil {
		t.Fatalf("socket not found: %v", err)
	}

	// Verify the socket mode is 0660 (SPEC §20.3).
	wantMode := os.FileMode(0660) | os.ModeSocket
	if info.Mode() != wantMode {
		t.Errorf("socket mode = %v, want %v", info.Mode(), wantMode)
	}

	cancel()
	if err := <-errCh; err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
}

// ---------- Server handles a pre-existing stale socket ----------

func TestServerHandlesStaleSocket(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "stale.sock")

	// Create a stale (non-listening) socket file.
	if err := os.WriteFile(sock, []byte("stale"), 0600); err != nil {
		t.Fatalf("create stale file: %v", err)
	}

	srv := NewServer(sock, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Start(ctx) }()

	waitForServer(t, sock)

	// The server should be reachable despite the previous stale file.
	client := httpClient(sock)
	resp, err := client.Get(baseURL() + "/v1/status")
	if err != nil {
		t.Fatalf("GET /v1/status: %v", err)
	}
	resp.Body.Close()

	cancel()
	if err := <-errCh; err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
}

// ---------- Unknown route returns a not_found envelope ----------

func TestServerUnknownRoute(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "test.sock")
	srv := NewServer(sock, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Start(ctx) }()

	waitForSocket(t, sock)

	client := httpClient(sock)
	resp, err := client.Get(baseURL() + "/v1/nonexistent")
	if err != nil {
		t.Fatalf("GET /v1/nonexistent: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusNotFound)
	}

	body, _ := io.ReadAll(resp.Body)
	apiErr, err := DecodeError(body)
	if err != nil {
		t.Fatalf("DecodeError: %v", err)
	}
	if apiErr.Code != ErrNotFound {
		t.Errorf("error code = %q, want %q", apiErr.Code, ErrNotFound)
	}

	cancel()
	<-errCh
}

// ---------- Shutdown removes the socket ----------

func TestServerShutdownRemovesSocket(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "test.sock")
	srv := NewServer(sock, nil)

	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Start(ctx) }()

	waitForSocket(t, sock)

	// Trigger shutdown.
	cancel()
	if err := <-errCh; err != nil {
		t.Fatalf("Start returned error: %v", err)
	}

	// The socket file should have been removed.
	if _, err := os.Stat(sock); !os.IsNotExist(err) {
		t.Errorf("socket file still exists after shutdown")
	}
}

// ---------- JSON content-type middleware ----------

func TestServerJSONContentType(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "test.sock")

	// Register a dummy handler to verify JSON headers.
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/ping", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{"pong":true}`)
	})
	srv := NewServer(sock, mux)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Start(ctx) }()

	waitForSocket(t, sock)

	client := httpClient(sock)
	resp, err := client.Get(baseURL() + "/v1/ping")
	if err != nil {
		t.Fatalf("GET /v1/ping: %v", err)
	}
	defer resp.Body.Close()

	ct := resp.Header.Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}

	cancel()
	<-errCh
}

// ---------- Routes without /v1 prefix return not_found ----------

func TestServerRoutesOutsideV1(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "test.sock")
	srv := NewServer(sock, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Start(ctx) }()

	waitForSocket(t, sock)

	client := httpClient(sock)

	// Root path should not match any handler.
	resp, err := client.Get(baseURL() + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("GET / status = %d, want %d", resp.StatusCode, http.StatusNotFound)
	}

	body, _ := io.ReadAll(resp.Body)
	var env ErrorEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if env.Error == nil || env.Error.Code != ErrNotFound {
		t.Errorf("expected not_found error, got %+v", env)
	}

	cancel()
	<-errCh
}

// ---------- helpers ----------

// waitForSocket polls until the socket file appears, failing the test after a
// timeout.
func waitForSocket(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("socket %s did not appear within timeout", path)
}

// waitForServer polls until a Unix connection to the socket succeeds, failing
// the test after a timeout. This is stronger than waitForSocket because it
// ensures the server is actually listening.
func waitForServer(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.Dial("unix", path)
		if err == nil {
			conn.Close()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("server at %s not reachable within timeout", path)
}
