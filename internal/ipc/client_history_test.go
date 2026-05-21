package ipc

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/deicod/uptimemonitor/internal/monitor"
	"github.com/deicod/uptimemonitor/internal/store/sqlite"
	"github.com/deicod/uptimemonitor/internal/store/tsdb"
)

// startHistoryTestServer wires a real IPC server with the history endpoint so
// client tests exercise the full HTTP round-trip over the Unix socket.
func startHistoryTestServer(t *testing.T, svc MonitorService, reader HistoryReader) *Client {
	t.Helper()
	sock := filepath.Join(t.TempDir(), "test.sock")
	startTestServer(t, sock, NewRouter(fakeStatusProvider{}, svc, nil, nil, WithHistory(reader)))
	return NewClient(sock)
}

// TestClientHistory pins the happy-path round-trip: the response body decodes
// into a typed HistoryResponse and the chosen range reaches the TSDB layer.
func TestClientHistory(t *testing.T) {
	start := time.Date(2026, 5, 21, 11, 55, 0, 0, time.UTC)
	reader := &fakeHistoryReader{points: []tsdb.HistoryPoint{{
		Start:         start,
		End:           start.Add(5 * time.Minute),
		State:         monitor.StateUp,
		SuccessRatio:  1.0,
		AvgDurationMS: 250,
	}}}
	svc := &fakeMonitorService{getResult: sampleMonitor()}
	client := startHistoryTestServer(t, svc, reader)

	got, err := client.History(context.Background(), "01HX", "6h")
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if got.Range != "6h" || got.Resolution != "5m" {
		t.Errorf("range/resolution = %q/%q, want 6h/5m", got.Range, got.Resolution)
	}
	if len(got.Points) != 1 {
		t.Fatalf("points len = %d, want 1", len(got.Points))
	}
	if got.Points[0].State != string(monitor.StateUp) || got.Points[0].AvgDurationMS != 250 {
		t.Errorf("point = %+v, want fixture", got.Points[0])
	}
	if reader.gotQuery.Range != tsdb.Range6h {
		t.Errorf("query range = %q, want 6h", reader.gotQuery.Range)
	}
}

// TestClientHistoryUnsupportedRange asserts the server's validation_error
// envelope is decoded as a typed APIError so callers can branch on Code.
func TestClientHistoryUnsupportedRange(t *testing.T) {
	svc := &fakeMonitorService{getResult: sampleMonitor()}
	client := startHistoryTestServer(t, svc, &fakeHistoryReader{})

	_, err := client.History(context.Background(), "01HX", "12h")
	if err == nil {
		t.Fatal("History should fail for unsupported range")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error type = %T, want *APIError", err)
	}
	if apiErr.Code != ErrValidation {
		t.Errorf("code = %q, want %q", apiErr.Code, ErrValidation)
	}
}

// TestClientHistoryNotFound asserts the typed not_found envelope round-trips.
func TestClientHistoryNotFound(t *testing.T) {
	svc := &fakeMonitorService{getErr: sqlite.ErrNotFound}
	client := startHistoryTestServer(t, svc, &fakeHistoryReader{})

	_, err := client.History(context.Background(), "01HXMISSING", "1h")
	if err == nil {
		t.Fatal("History should fail for missing monitor")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error type = %T, want *APIError", err)
	}
	if apiErr.Code != ErrNotFound {
		t.Errorf("code = %q, want %q", apiErr.Code, ErrNotFound)
	}
}
