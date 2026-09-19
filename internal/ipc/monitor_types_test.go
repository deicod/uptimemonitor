package ipc_test

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/deicod/uptimemonitor/internal/ipc"
	"github.com/deicod/uptimemonitor/internal/monitor"
	"github.com/deicod/uptimemonitor/internal/store/sqlite"
)

// startMonitorAPI serves the /v1 monitor and check-result endpoints over a
// Unix socket, backed by a migrated SQLite store and the real monitor service
// (and so the real validator). It returns the socket path for raw requests.
func startMonitorAPI(t *testing.T) (*ipc.Client, *sqlite.Store, string) {
	t.Helper()
	store, err := sqlite.Open(filepath.Join(t.TempDir(), "config.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	svc := monitor.NewService(sqlite.NewMonitorRepo(store), sqlite.NewMonitorStateRepo(store), sqlite.NewEventRepo(store))
	mux := ipc.NewRouter(nil, svc, sqlite.NewIncidentRepo(store), sqlite.NewEventRepo(store),
		ipc.WithCheckResults(sqlite.NewCheckResultRepo(store)))

	sock := filepath.Join(t.TempDir(), "test.sock")
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- ipc.NewServer(sock, mux).Start(ctx) }()
	t.Cleanup(func() {
		cancel()
		<-errCh
	})
	waitForServer(t, sock)
	return ipc.NewClient(sock), store, sock
}

// getRawChecks fetches a monitor's recent checks over the socket as raw JSON
// rows, bypassing the typed client the way a v0.1.0 consumer would.
func getRawChecks(t *testing.T, sock, monitorID string) []map[string]json.RawMessage {
	t.Helper()
	hc := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", sock)
		},
	}}
	resp, err := hc.Get("http://uptimemonitor/v1/monitors/" + monitorID + "/checks")
	if err != nil {
		t.Fatalf("GET checks: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var body struct {
		Checks []map[string]json.RawMessage `json:"checks"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode checks: %v", err)
	}
	return body.Checks
}

// sameJSON reports whether a and b encode the same JSON value.
func sameJSON(t *testing.T, a, b []byte) bool {
	t.Helper()
	var va, vb any
	if err := json.Unmarshal(a, &va); err != nil {
		t.Fatalf("decode %s: %v", a, err)
	}
	if err := json.Unmarshal(b, &vb); err != nil {
		t.Fatalf("decode %s: %v", b, err)
	}
	return reflect.DeepEqual(va, vb)
}

// TestTCPAndDNSMonitorsOverIPC walks the operator scenario from the v0.2.0
// brief through the /v1 API: an SSH port monitor and an authoritative SOA
// monitor for one name server are created, read back with their configs
// intact, edited, and deleted.
func TestTCPAndDNSMonitorsOverIPC(t *testing.T) {
	client, _, _ := startMonitorAPI(t)
	ctx := context.Background()

	tcpCfg := json.RawMessage(`{"host":"ns1.dysv.de","port":22}`)
	dnsCfg := json.RawMessage(`{"name":"dysv.de","record_type":"SOA","resolver":"ns1.dysv.de",` +
		`"expected_value":{"condition":"starts_with","value":"ns1.dysv.de. "}}`)

	tcpMon, err := client.CreateMonitor(ctx, ipc.CreateMonitorRequest{
		Name: "ns1 SSH", Type: "tcp", Enabled: true,
		Interval: ipc.Duration(time.Minute), Timeout: ipc.Duration(5 * time.Second), Config: tcpCfg,
	})
	if err != nil {
		t.Fatalf("create tcp monitor: %v", err)
	}
	dnsMon, err := client.CreateMonitor(ctx, ipc.CreateMonitorRequest{
		Name: "ns1 DNS authoritative", Type: "dns", Enabled: true, NotificationsEnabled: true,
		Interval: ipc.Duration(time.Minute), Timeout: ipc.Duration(5 * time.Second), Config: dnsCfg,
	})
	if err != nil {
		t.Fatalf("create dns monitor: %v", err)
	}

	for _, want := range []struct {
		id, typ string
		cfg     json.RawMessage
	}{{tcpMon.ID, "tcp", tcpCfg}, {dnsMon.ID, "dns", dnsCfg}} {
		got, err := client.GetMonitor(ctx, want.id)
		if err != nil {
			t.Fatalf("get %s: %v", want.typ, err)
		}
		if got.Type != want.typ || !sameJSON(t, got.Config, want.cfg) {
			t.Errorf("get %s = type %q config %s, want config %s", want.typ, got.Type, got.Config, want.cfg)
		}
	}
	list, err := client.ListMonitors(ctx, ipc.MonitorListFilter{})
	if err != nil || len(list) != 2 {
		t.Fatalf("list = %d monitors, %v; want 2", len(list), err)
	}

	// Pointing the monitor at ns2 and dropping the expected value must not
	// leave the old expected value behind.
	edited := json.RawMessage(`{"name":"dysv.de","record_type":"SOA","resolver":"ns2.dysv.de"}`)
	if _, err := client.UpdateMonitor(ctx, dnsMon.ID, ipc.UpdateMonitorRequest{Config: edited}); err != nil {
		t.Fatalf("update dns monitor: %v", err)
	}
	got, err := client.GetMonitor(ctx, dnsMon.ID)
	if err != nil || !sameJSON(t, got.Config, edited) {
		t.Errorf("after update config = %s (%v), want %s", got.Config, err, edited)
	}

	if err := client.DeleteMonitor(ctx, tcpMon.ID); err != nil {
		t.Fatalf("delete tcp monitor: %v", err)
	}
	var apiErr *ipc.APIError
	if _, err := client.GetMonitor(ctx, tcpMon.ID); !errors.As(err, &apiErr) || apiErr.Code != ipc.ErrNotFound {
		t.Errorf("get deleted monitor err = %v, want not_found", err)
	}
}

// TestTCPAndDNSValidationErrorsOverIPC checks invalid type-specific configs
// come back as validation_error naming the offending config field, which is
// what the TUI uses to put the message next to the right input.
func TestTCPAndDNSValidationErrorsOverIPC(t *testing.T) {
	client, _, _ := startMonitorAPI(t)
	for _, tc := range []struct {
		name, typ, cfg, field string
	}{
		{"tcp port zero", "tcp", `{"host":"ns1.dysv.de","port":0}`, "port"},
		{"tcp port too high", "tcp", `{"host":"ns1.dysv.de","port":65536}`, "port"},
		{"tcp bad host", "tcp", `{"host":"ns1 dysv.de","port":22}`, "host"},
		{"tcp missing host", "tcp", `{"port":22}`, "host"},
		{"dns missing query name", "dns", `{"record_type":"SOA"}`, "config.name"},
		{"dns bad query name", "dns", `{"name":"dysv..de","record_type":"SOA"}`, "config.name"},
		{"dns unsupported type", "dns", `{"name":"dysv.de","record_type":"ANY"}`, "record_type"},
		{"dns bad resolver", "dns", `{"name":"dysv.de","record_type":"SOA","resolver":"ns1.dysv.de:dns"}`, "resolver"},
		{"dns bad condition", "dns", `{"name":"dysv.de","record_type":"A","expected_value":{"condition":"matches","value":"x"}}`, "expected_value.condition"},
		{"dns empty expected value", "dns", `{"name":"dysv.de","record_type":"A","expected_value":{"condition":"equals","value":""}}`, "expected_value.value"},
		{"config wrong shape", "tcp", `{"host":"ns1.dysv.de","port":"22"}`, "config"},
		// No runner can execute ping yet, so the API refuses it outright.
		{"ping not available", "ping", `{"host":"192.0.2.1"}`, "type"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := client.CreateMonitor(context.Background(), ipc.CreateMonitorRequest{
				Name: "invalid", Type: tc.typ, Enabled: true,
				Interval: ipc.Duration(time.Minute), Timeout: ipc.Duration(5 * time.Second), Config: json.RawMessage(tc.cfg),
			})
			var apiErr *ipc.APIError
			if !errors.As(err, &apiErr) || apiErr.Code != ipc.ErrValidation || apiErr.Field != tc.field {
				t.Errorf("create err = %v, want validation_error on field %q", err, tc.field)
			}
		})
	}
	if list, err := client.ListMonitors(context.Background(), ipc.MonitorListFilter{}); err != nil || len(list) != 0 {
		t.Errorf("list after invalid creates = %d monitors (%v), want none persisted", len(list), err)
	}
}

// TestRecentChecksReturnDetails checks GET /v1/monitors/{id}/checks over
// the real stack: every row carries its type-specific details untouched
// (SPEC §15.3), HTTP rows additionally carry the deprecated http_status_code
// that v0.1.0 consumers read (SPEC §10.4), and SQLite stores only details.
func TestRecentChecksReturnDetails(t *testing.T) {
	client, store, sock := startMonitorAPI(t)
	ctx := context.Background()
	for _, tc := range []struct {
		typ, config, details string
		wantStatus           string // raw http_status_code; "" = absent
	}{
		{"http", `{"url":"https://example.com","method":"GET","expected_status_min":200,"expected_status_max":299}`,
			`{"status_code":200}`, "200"},
		{"tcp", `{"host":"ns1.dysv.de","port":22}`, `{"remote_addr":"192.0.2.53:22"}`, ""},
		{"dns", `{"name":"dysv.de","record_type":"SOA","resolver":"ns1.dysv.de"}`,
			`{"name":"dysv.de","record_type":"SOA","resolver":"ns1.dysv.de:53","server":"192.0.2.53:53",` +
				`"rcode":"NOERROR","answer_count":1,"records":["ns1.dysv.de. hostmaster.dysv.de. 2026091901 7200 3600 1209600 3600"]}`, ""},
	} {
		t.Run(tc.typ, func(t *testing.T) {
			mon, err := client.CreateMonitor(ctx, ipc.CreateMonitorRequest{
				Name: tc.typ + " monitor", Type: tc.typ, Enabled: true,
				Interval: ipc.Duration(time.Minute), Timeout: ipc.Duration(5 * time.Second), Config: json.RawMessage(tc.config),
			})
			if err != nil {
				t.Fatalf("create: %v", err)
			}
			now := time.Now().UTC()
			checkID := monitor.NewID()
			if err := sqlite.NewCheckResultRepo(store).Insert(ctx, &monitor.CheckResult{
				ID: checkID, MonitorID: mon.ID, StartedAt: now, FinishedAt: now,
				Duration: 12 * time.Millisecond, Success: true, State: monitor.StateUp, Details: json.RawMessage(tc.details),
			}); err != nil {
				t.Fatalf("insert check: %v", err)
			}

			checks, err := client.RecentChecks(ctx, mon.ID, 10)
			if err != nil || len(checks) != 1 || !sameJSON(t, checks[0].Details, json.RawMessage(tc.details)) {
				t.Fatalf("RecentChecks = %+v, %v; want one row with details %s", checks, err, tc.details)
			}

			rows := getRawChecks(t, sock, mon.ID)
			if len(rows) != 1 || !sameJSON(t, rows[0]["details"], json.RawMessage(tc.details)) {
				t.Fatalf("raw rows = %v, want one row with details %s", rows, tc.details)
			}
			status, has := rows[0]["http_status_code"]
			if tc.wantStatus == "" && has {
				t.Errorf("raw http_status_code = %s, want it absent for %s", status, tc.typ)
			}
			if tc.wantStatus != "" && string(status) != tc.wantStatus {
				t.Errorf("raw http_status_code = %q, want %s", status, tc.wantStatus)
			}

			// The status is derived on read: the stored row holds only details.
			var stored string
			if err := store.DB().QueryRowContext(ctx, "SELECT details FROM check_results WHERE id = ?", checkID).Scan(&stored); err != nil {
				t.Fatalf("read stored details: %v", err)
			}
			if stored != tc.details {
				t.Errorf("stored details = %s, want %s", stored, tc.details)
			}
		})
	}
}
