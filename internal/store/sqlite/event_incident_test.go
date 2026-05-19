package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/deicod/uptimemonitor/internal/monitor"
)

// TestEventRepoListOrdering verifies events come back newest first, both
// globally and scoped to a monitor — the TUI audit log relies on this order,
// so a regression here would silently show events out of sequence.
func TestEventRepoListOrdering(t *testing.T) {
	store := openMigrated(t)
	ctx := context.Background()
	monitorID := insertMonitor(t, store)
	repo := NewEventRepo(store)

	base := time.Now().UTC().Truncate(time.Second).Add(-time.Hour)
	// A service-scoped event (nil MonitorID) plus three monitor-scoped ones.
	service := &monitor.Event{
		ID:        monitor.NewID(),
		Type:      monitor.EventServiceStarted,
		CreatedAt: base,
	}
	if err := repo.Insert(ctx, service); err != nil {
		t.Fatalf("Insert service event: %v", err)
	}
	var monitorEventIDs []string
	for i := 0; i < 3; i++ {
		e := &monitor.Event{
			ID:        monitor.NewID(),
			Type:      monitor.EventMonitorStateChanged,
			MonitorID: &monitorID,
			CreatedAt: base.Add(time.Duration(i+1) * time.Minute),
		}
		if err := repo.Insert(ctx, e); err != nil {
			t.Fatalf("Insert monitor event: %v", err)
		}
		monitorEventIDs = append(monitorEventIDs, e.ID)
	}

	// Global list: all four, newest first.
	all, err := repo.List(ctx, 10)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 4 {
		t.Fatalf("List returned %d events, want 4", len(all))
	}
	wantGlobal := []string{monitorEventIDs[2], monitorEventIDs[1], monitorEventIDs[0], service.ID}
	for i, id := range wantGlobal {
		if all[i].ID != id {
			t.Errorf("global position %d = %s, want %s", i, all[i].ID, id)
		}
	}
	if all[3].MonitorID != nil {
		t.Errorf("service event MonitorID = %v, want nil", all[3].MonitorID)
	}

	// Monitor-scoped list excludes the service event.
	scoped, err := repo.ListByMonitor(ctx, monitorID, 10)
	if err != nil {
		t.Fatalf("ListByMonitor: %v", err)
	}
	if len(scoped) != 3 {
		t.Fatalf("ListByMonitor returned %d events, want 3", len(scoped))
	}
	for i, id := range []string{monitorEventIDs[2], monitorEventIDs[1], monitorEventIDs[0]} {
		if scoped[i].ID != id {
			t.Errorf("scoped position %d = %s, want %s", i, scoped[i].ID, id)
		}
	}

	// The limit caps the slice.
	limited, err := repo.List(ctx, 2)
	if err != nil {
		t.Fatalf("List(limit=2): %v", err)
	}
	if len(limited) != 2 {
		t.Errorf("List(limit=2) returned %d, want 2", len(limited))
	}
}

// TestEventRepoDataRoundTrip verifies the JSON data payload survives a write
// and read, and that an unset payload is stored as an empty object rather than
// failing the NOT NULL data_json column.
func TestEventRepoDataRoundTrip(t *testing.T) {
	store := openMigrated(t)
	ctx := context.Background()
	repo := NewEventRepo(store)

	withData := &monitor.Event{
		ID:        monitor.NewID(),
		Type:      monitor.EventMonitorCreated,
		Data:      json.RawMessage(`{"name":"example"}`),
		CreatedAt: time.Now().UTC().Truncate(time.Second),
	}
	noData := &monitor.Event{
		ID:        monitor.NewID(),
		Type:      monitor.EventServiceStopped,
		CreatedAt: withData.CreatedAt.Add(time.Minute),
	}
	for _, e := range []*monitor.Event{withData, noData} {
		if err := repo.Insert(ctx, e); err != nil {
			t.Fatalf("Insert: %v", err)
		}
	}

	got, err := repo.List(ctx, 10)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("List returned %d, want 2", len(got))
	}
	// got[0] is noData (newer), got[1] is withData.
	if string(got[0].Data) != "{}" {
		t.Errorf("unset data = %q, want {}", got[0].Data)
	}
	if string(got[1].Data) != `{"name":"example"}` {
		t.Errorf("data round-trip = %q, want the inserted payload", got[1].Data)
	}
}

// TestIncidentRepoLifecycle verifies the open → resolve lifecycle: an open
// incident is found by FindOpenByMonitor, resolving it stamps the resolution
// fields, and a resolved incident is no longer reported as open.
func TestIncidentRepoLifecycle(t *testing.T) {
	store := openMigrated(t)
	ctx := context.Background()
	monitorID := insertMonitor(t, store)
	repo := NewIncidentRepo(store)

	startEventID := monitor.NewID()
	started := time.Now().UTC().Truncate(time.Second)
	in := &monitor.Incident{
		ID:           monitor.NewID(),
		MonitorID:    monitorID,
		StartedAt:    started,
		StartEventID: startEventID,
		Reason:       "connection refused",
	}
	if err := repo.Open(ctx, in); err != nil {
		t.Fatalf("Open: %v", err)
	}

	open, err := repo.FindOpenByMonitor(ctx, monitorID)
	if err != nil {
		t.Fatalf("FindOpenByMonitor: %v", err)
	}
	if open.ID != in.ID || open.StartEventID != startEventID || open.Reason != "connection refused" {
		t.Errorf("open incident mismatch: got %+v", open)
	}
	if open.ResolvedAt != nil || open.EndEventID != nil {
		t.Errorf("freshly opened incident already resolved: %+v", open)
	}

	// Resolve it.
	endEventID := monitor.NewID()
	resolved := started.Add(5 * time.Minute)
	if err := repo.Resolve(ctx, in.ID, resolved, endEventID); err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	// No open incident remains.
	if _, err := repo.FindOpenByMonitor(ctx, monitorID); !errors.Is(err, ErrNotFound) {
		t.Errorf("FindOpenByMonitor after resolve = %v, want ErrNotFound", err)
	}

	// The resolved incident still lists, now carrying its resolution fields.
	list, err := repo.List(ctx, monitorID, 10)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("List returned %d incidents, want 1", len(list))
	}
	got := list[0]
	if got.ResolvedAt == nil || !got.ResolvedAt.Equal(resolved) {
		t.Errorf("ResolvedAt = %v, want %v", got.ResolvedAt, resolved)
	}
	if got.EndEventID == nil || *got.EndEventID != endEventID {
		t.Errorf("EndEventID = %v, want %s", got.EndEventID, endEventID)
	}
}

// TestIncidentRepoResolveOnceOnly verifies that resolving an already-resolved
// or unknown incident yields ErrNotFound, so the state machine cannot
// double-resolve and produce a second recovery event.
func TestIncidentRepoResolveOnceOnly(t *testing.T) {
	store := openMigrated(t)
	ctx := context.Background()
	monitorID := insertMonitor(t, store)
	repo := NewIncidentRepo(store)

	in := &monitor.Incident{
		ID:           monitor.NewID(),
		MonitorID:    monitorID,
		StartedAt:    time.Now().UTC().Truncate(time.Second),
		StartEventID: monitor.NewID(),
	}
	if err := repo.Open(ctx, in); err != nil {
		t.Fatalf("Open: %v", err)
	}

	now := time.Now().UTC()
	if err := repo.Resolve(ctx, in.ID, now, monitor.NewID()); err != nil {
		t.Fatalf("first Resolve: %v", err)
	}
	if err := repo.Resolve(ctx, in.ID, now, monitor.NewID()); !errors.Is(err, ErrNotFound) {
		t.Errorf("second Resolve = %v, want ErrNotFound", err)
	}
	if err := repo.Resolve(ctx, monitor.NewID(), now, ""); !errors.Is(err, ErrNotFound) {
		t.Errorf("Resolve(unknown) = %v, want ErrNotFound", err)
	}
}

// TestIncidentRepoListByMonitor verifies List is scoped to one monitor and
// orders incidents newest first.
func TestIncidentRepoListByMonitor(t *testing.T) {
	store := openMigrated(t)
	ctx := context.Background()
	monitorID := insertMonitor(t, store)
	repo := NewIncidentRepo(store)

	base := time.Now().UTC().Truncate(time.Second).Add(-time.Hour)
	var ids []string
	for i := 0; i < 3; i++ {
		in := &monitor.Incident{
			ID:           monitor.NewID(),
			MonitorID:    monitorID,
			StartedAt:    base.Add(time.Duration(i) * time.Minute),
			StartEventID: monitor.NewID(),
		}
		if err := repo.Open(ctx, in); err != nil {
			t.Fatalf("Open: %v", err)
		}
		ids = append(ids, in.ID)
	}

	list, err := repo.List(ctx, monitorID, 10)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("List returned %d, want 3", len(list))
	}
	for i, id := range []string{ids[2], ids[1], ids[0]} {
		if list[i].ID != id {
			t.Errorf("position %d = %s, want %s", i, list[i].ID, id)
		}
	}

	// A monitor with no incidents lists empty, not an error.
	other := insertOtherMonitor(t, store)
	empty, err := repo.List(ctx, other, 10)
	if err != nil {
		t.Fatalf("List(other): %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("List(other) returned %d, want 0", len(empty))
	}
}

// insertOtherMonitor persists a second monitor with a distinct ID so a test
// can assert per-monitor scoping.
func insertOtherMonitor(t *testing.T, store *Store) string {
	t.Helper()

	m := sampleMonitor(t)
	if err := NewMonitorRepo(store).Insert(context.Background(), m); err != nil {
		t.Fatalf("Insert other monitor: %v", err)
	}
	return m.ID
}
