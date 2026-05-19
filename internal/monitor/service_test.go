package monitor_test

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/deicod/uptimemonitor/internal/monitor"
	"github.com/deicod/uptimemonitor/internal/store/sqlite"
)

// newService opens a migrated temp database and returns a monitor service
// backed by real SQLite repositories, plus the store so a test can inspect the
// persisted rows directly.
func newService(t *testing.T) (*monitor.Service, *sqlite.Store) {
	t.Helper()

	store, err := sqlite.Open(filepath.Join(t.TempDir(), "config.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	if err := store.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	svc := monitor.NewService(
		sqlite.NewMonitorRepo(store),
		sqlite.NewMonitorStateRepo(store),
		sqlite.NewEventRepo(store),
	)
	return svc, store
}

// sampleMonitor builds a valid, enabled HTTP monitor with no ID — the service
// is responsible for assigning the ID and timestamps.
func sampleMonitor(t *testing.T) *monitor.Monitor {
	t.Helper()

	cfg, err := json.Marshal(monitor.HTTPMonitorConfig{
		URL:               "https://example.com",
		Method:            "GET",
		ExpectedStatusMin: 200,
		ExpectedStatusMax: 299,
	})
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	return &monitor.Monitor{
		Name:                 "Example",
		Type:                 monitor.MonitorTypeHTTP,
		Enabled:              true,
		Interval:             60 * time.Second,
		Timeout:              10 * time.Second,
		Config:               cfg,
		NotificationsEnabled: true,
	}
}

// TestServiceCreatePersists verifies that Create writes all three rows a new
// monitor needs: the monitor itself, an initial monitor_states row, and a
// monitor_created event. A scheduler started later relies on the state row
// existing, so a missing one is a real bug.
func TestServiceCreatePersists(t *testing.T) {
	svc, store := newService(t)
	ctx := context.Background()

	m, err := svc.Create(ctx, sampleMonitor(t))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if m.ID == "" {
		t.Fatal("Create did not assign an ID")
	}
	if m.CreatedAt.IsZero() || m.UpdatedAt.IsZero() {
		t.Fatal("Create did not stamp timestamps")
	}

	if _, err := sqlite.NewMonitorRepo(store).Get(ctx, m.ID); err != nil {
		t.Fatalf("monitor not persisted: %v", err)
	}

	st, err := sqlite.NewMonitorStateRepo(store).Get(ctx, m.ID)
	if err != nil {
		t.Fatalf("monitor_state not persisted: %v", err)
	}
	if st.State != monitor.StateUnknown {
		t.Errorf("initial state = %q, want %q", st.State, monitor.StateUnknown)
	}

	events, err := sqlite.NewEventRepo(store).ListByMonitor(ctx, m.ID, 0)
	if err != nil {
		t.Fatalf("ListByMonitor: %v", err)
	}
	if len(events) != 1 || events[0].Type != monitor.EventMonitorCreated {
		t.Fatalf("events = %+v, want one %q", events, monitor.EventMonitorCreated)
	}
}

// TestServiceCreateDisabledIsPaused verifies that a monitor created disabled
// starts in the paused state, not unknown (SPEC §17.2: any + disabled -> paused).
func TestServiceCreateDisabledIsPaused(t *testing.T) {
	svc, store := newService(t)
	ctx := context.Background()

	in := sampleMonitor(t)
	in.Enabled = false
	m, err := svc.Create(ctx, in)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	st, err := sqlite.NewMonitorStateRepo(store).Get(ctx, m.ID)
	if err != nil {
		t.Fatalf("Get state: %v", err)
	}
	if st.State != monitor.StatePaused {
		t.Errorf("state = %q, want %q", st.State, monitor.StatePaused)
	}
}

// TestServiceCreateValidationError verifies that Create rejects invalid input
// and persists nothing.
func TestServiceCreateValidationError(t *testing.T) {
	svc, store := newService(t)
	ctx := context.Background()

	in := sampleMonitor(t)
	in.Name = ""
	if _, err := svc.Create(ctx, in); err == nil {
		t.Fatal("Create accepted a monitor with an empty name")
	} else {
		var fe *monitor.FieldError
		if !errors.As(err, &fe) || fe.Field != "name" {
			t.Errorf("error = %v, want a FieldError on \"name\"", err)
		}
	}

	list, err := sqlite.NewMonitorRepo(store).List(ctx, sqlite.MonitorFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("invalid Create persisted %d monitors", len(list))
	}
}

// TestServiceUpdateRevalidates verifies that Update re-runs validation on the
// merged monitor and emits a monitor_updated event on success.
func TestServiceUpdateRevalidates(t *testing.T) {
	svc, store := newService(t)
	ctx := context.Background()

	m, err := svc.Create(ctx, sampleMonitor(t))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// A bad config must be rejected even though the monitor already exists.
	bad := *m
	bad.Config = json.RawMessage(`{"url":"not-a-url","method":"GET","expected_status_min":200,"expected_status_max":299}`)
	if _, err := svc.Update(ctx, &bad); err == nil {
		t.Fatal("Update accepted an invalid URL")
	} else {
		var fe *monitor.FieldError
		if !errors.As(err, &fe) || fe.Field != "url" {
			t.Errorf("error = %v, want a FieldError on \"url\"", err)
		}
	}

	// A valid change is persisted and audited.
	good := *m
	good.Name = "Renamed"
	updated, err := svc.Update(ctx, &good)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Name != "Renamed" {
		t.Errorf("Name = %q, want %q", updated.Name, "Renamed")
	}

	events, err := sqlite.NewEventRepo(store).ListByMonitor(ctx, m.ID, 0)
	if err != nil {
		t.Fatalf("ListByMonitor: %v", err)
	}
	if len(events) != 2 || events[0].Type != monitor.EventMonitorUpdated {
		t.Fatalf("events = %+v, want newest %q", events, monitor.EventMonitorUpdated)
	}
}

// TestServiceUpdatePreservesEnabled verifies that Update does not flip the
// enabled flag — that transition belongs to Enable/Disable so the state row
// and audit log stay consistent.
func TestServiceUpdatePreservesEnabled(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()

	m, err := svc.Create(ctx, sampleMonitor(t))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	patch := *m
	patch.Enabled = false
	updated, err := svc.Update(ctx, &patch)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if !updated.Enabled {
		t.Error("Update flipped Enabled; that must only happen via Disable")
	}
}

// TestServiceDelete verifies that Delete soft-deletes the monitor and records
// a monitor_deleted event.
func TestServiceDelete(t *testing.T) {
	svc, store := newService(t)
	ctx := context.Background()

	m, err := svc.Create(ctx, sampleMonitor(t))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := svc.Delete(ctx, m.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if _, err := svc.Get(ctx, m.ID); !errors.Is(err, sqlite.ErrNotFound) {
		t.Errorf("Get after Delete = %v, want ErrNotFound", err)
	}

	events, err := sqlite.NewEventRepo(store).ListByMonitor(ctx, m.ID, 0)
	if err != nil {
		t.Fatalf("ListByMonitor: %v", err)
	}
	if len(events) != 2 || events[0].Type != monitor.EventMonitorDeleted {
		t.Fatalf("events = %+v, want newest %q", events, monitor.EventMonitorDeleted)
	}
}

// TestServiceEnableDisable verifies that Disable and Enable flip both the
// monitor flag and the monitor_states row, and emit the matching events.
func TestServiceEnableDisable(t *testing.T) {
	svc, store := newService(t)
	ctx := context.Background()
	stateRepo := sqlite.NewMonitorStateRepo(store)

	m, err := svc.Create(ctx, sampleMonitor(t))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	disabled, err := svc.Disable(ctx, m.ID)
	if err != nil {
		t.Fatalf("Disable: %v", err)
	}
	if disabled.Enabled {
		t.Error("Disable left the monitor enabled")
	}
	if st, _ := stateRepo.Get(ctx, m.ID); st.State != monitor.StatePaused {
		t.Errorf("state after Disable = %q, want %q", st.State, monitor.StatePaused)
	}

	enabled, err := svc.Enable(ctx, m.ID)
	if err != nil {
		t.Fatalf("Enable: %v", err)
	}
	if !enabled.Enabled {
		t.Error("Enable left the monitor disabled")
	}
	if st, _ := stateRepo.Get(ctx, m.ID); st.State != monitor.StateUnknown {
		t.Errorf("state after Enable = %q, want %q", st.State, monitor.StateUnknown)
	}

	events, err := sqlite.NewEventRepo(store).ListByMonitor(ctx, m.ID, 0)
	if err != nil {
		t.Fatalf("ListByMonitor: %v", err)
	}
	// created, disabled, enabled
	if len(events) != 3 ||
		events[0].Type != monitor.EventMonitorEnabled ||
		events[1].Type != monitor.EventMonitorDisabled {
		t.Fatalf("events = %+v, want enabled then disabled then created", events)
	}
}

// TestServiceEnableIdempotent verifies that enabling an already-enabled monitor
// is a no-op: no spurious event, no state churn.
func TestServiceEnableIdempotent(t *testing.T) {
	svc, store := newService(t)
	ctx := context.Background()

	m, err := svc.Create(ctx, sampleMonitor(t))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := svc.Enable(ctx, m.ID); err != nil {
		t.Fatalf("Enable: %v", err)
	}

	events, err := sqlite.NewEventRepo(store).ListByMonitor(ctx, m.ID, 0)
	if err != nil {
		t.Fatalf("ListByMonitor: %v", err)
	}
	if len(events) != 1 {
		t.Errorf("re-enabling an enabled monitor emitted %d events, want 1", len(events))
	}
}

// TestServiceOnChange verifies that the OnChange observer receives a Change for
// each lifecycle operation, which is how the scheduler stays in sync (M7).
func TestServiceOnChange(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()

	var changes []monitor.Change
	svc.OnChange = func(c monitor.Change) { changes = append(changes, c) }

	m, err := svc.Create(ctx, sampleMonitor(t))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := svc.Disable(ctx, m.ID); err != nil {
		t.Fatalf("Disable: %v", err)
	}

	want := []monitor.ChangeKind{monitor.ChangeCreated, monitor.ChangeDisabled}
	if len(changes) != len(want) {
		t.Fatalf("got %d changes, want %d", len(changes), len(want))
	}
	for i, k := range want {
		if changes[i].Kind != k {
			t.Errorf("change[%d].Kind = %q, want %q", i, changes[i].Kind, k)
		}
		if changes[i].Monitor == nil || changes[i].Monitor.ID != m.ID {
			t.Errorf("change[%d] missing monitor %s", i, m.ID)
		}
	}
}
