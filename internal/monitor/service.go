package monitor

import (
	"context"
	"encoding/json"
	"time"
)

// MonitorFilter narrows a monitor List query. A nil field means "no
// constraint". It lives in this package so the Repo interface can reference it
// without the monitor package importing internal/store (which would create an
// import cycle); the SQLite repository aliases this type.
type MonitorFilter struct {
	// Enabled, when set, keeps only monitors with the given enabled flag.
	Enabled *bool
	// State, when set, keeps only monitors whose current state matches.
	State *MonitorState
}

// Repo is the persistence contract the Service needs for the monitors table.
// *sqlite.MonitorRepo satisfies it; the interface is declared here so the
// service does not import internal/store and create an import cycle.
type Repo interface {
	Insert(ctx context.Context, m *Monitor) error
	Get(ctx context.Context, id string) (*Monitor, error)
	List(ctx context.Context, f MonitorFilter) ([]*Monitor, error)
	Update(ctx context.Context, m *Monitor) error
	SoftDelete(ctx context.Context, id string) error
}

// StateRepo is the persistence contract the Service needs for monitor_states.
type StateRepo interface {
	Upsert(ctx context.Context, st *MonitorStatus) error
	Get(ctx context.Context, monitorID string) (*MonitorStatus, error)
}

// EventRepo is the persistence contract the Service needs for the audit log.
type EventRepo interface {
	Insert(ctx context.Context, e *Event) error
}

// ChangeKind identifies which lifecycle operation produced a Change.
type ChangeKind string

// ChangeKind values.
const (
	ChangeCreated  ChangeKind = "created"
	ChangeUpdated  ChangeKind = "updated"
	ChangeDeleted  ChangeKind = "deleted"
	ChangeEnabled  ChangeKind = "enabled"
	ChangeDisabled ChangeKind = "disabled"
)

// Change describes a monitor lifecycle event delivered to an OnChange observer.
type Change struct {
	Kind    ChangeKind
	Monitor *Monitor
}

// Service orchestrates the monitor repositories: it validates input, keeps the
// monitor_states row and the events audit log in step with the monitors table,
// and notifies an optional observer so the scheduler can react (M7).
type Service struct {
	monitors Repo
	states   StateRepo
	events   EventRepo

	// OnChange, when non-nil, is invoked after a monitor is created, updated,
	// deleted, enabled, or disabled. The scheduler subscribes to it to keep its
	// per-monitor schedule in sync; it is nil until a subscriber sets it.
	OnChange func(Change)
}

// NewService binds a Service to its repositories.
func NewService(monitors Repo, states StateRepo, events EventRepo) *Service {
	return &Service{monitors: monitors, states: states, events: events}
}

// Create validates the monitor, assigns its ID and timestamps, persists it
// along with an initial monitor_states row, records a monitor_created event,
// and notifies the OnChange observer. A monitor created disabled starts in the
// paused state (SPEC §17.2).
func (s *Service) Create(ctx context.Context, m *Monitor) (*Monitor, error) {
	if err := validateInput(m); err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	m.ID = NewID()
	m.CreatedAt = now
	m.UpdatedAt = now
	m.DeletedAt = nil

	if err := s.monitors.Insert(ctx, m); err != nil {
		return nil, err
	}

	state := StateUnknown
	if !m.Enabled {
		state = StatePaused
	}
	if err := s.states.Upsert(ctx, &MonitorStatus{
		MonitorID: m.ID,
		State:     state,
		UpdatedAt: now,
	}); err != nil {
		return nil, err
	}

	if err := s.recordEvent(ctx, EventMonitorCreated, m.ID, now); err != nil {
		return nil, err
	}
	s.notify(ChangeCreated, m)
	return m, nil
}

// Get returns the monitor with the given ID.
func (s *Service) Get(ctx context.Context, id string) (*Monitor, error) {
	return s.monitors.Get(ctx, id)
}

// List returns the monitors matching the filter.
func (s *Service) List(ctx context.Context, f MonitorFilter) ([]*Monitor, error) {
	return s.monitors.List(ctx, f)
}

// Update re-validates and persists changes to an existing monitor, then records
// a monitor_updated event. The caller supplies the full desired monitor; the
// enabled flag and creation metadata are preserved from the stored record
// because the enabled state changes only through Enable/Disable.
func (s *Service) Update(ctx context.Context, m *Monitor) (*Monitor, error) {
	existing, err := s.monitors.Get(ctx, m.ID)
	if err != nil {
		return nil, err
	}
	m.Enabled = existing.Enabled
	m.CreatedAt = existing.CreatedAt
	m.DeletedAt = existing.DeletedAt

	if err := validateInput(m); err != nil {
		return nil, err
	}
	m.UpdatedAt = time.Now().UTC()
	if err := s.monitors.Update(ctx, m); err != nil {
		return nil, err
	}

	if err := s.recordEvent(ctx, EventMonitorUpdated, m.ID, m.UpdatedAt); err != nil {
		return nil, err
	}
	s.notify(ChangeUpdated, m)
	return m, nil
}

// Delete soft-deletes a monitor and records a monitor_deleted event. The
// monitor row is kept so historical incidents and TSDB samples keep a valid
// referent (SPEC §6 decision 2).
func (s *Service) Delete(ctx context.Context, id string) error {
	m, err := s.monitors.Get(ctx, id)
	if err != nil {
		return err
	}
	if err := s.monitors.SoftDelete(ctx, id); err != nil {
		return err
	}
	now := time.Now().UTC()
	if err := s.recordEvent(ctx, EventMonitorDeleted, id, now); err != nil {
		return err
	}
	s.notify(ChangeDeleted, m)
	return nil
}

// Enable resumes a disabled monitor: the state moves to unknown so the next
// check reclassifies it (SPEC §17.2). Enabling an already-enabled monitor is a
// no-op.
func (s *Service) Enable(ctx context.Context, id string) (*Monitor, error) {
	return s.setEnabled(ctx, id, true)
}

// Disable pauses a monitor: the state moves to paused and the scheduler stops
// checking it. Disabling an already-disabled monitor is a no-op.
func (s *Service) Disable(ctx context.Context, id string) (*Monitor, error) {
	return s.setEnabled(ctx, id, false)
}

// setEnabled flips a monitor's enabled flag, moves its state row to unknown or
// paused, records the matching event, and notifies the observer. It returns
// early without side effects when the monitor is already in the target state.
func (s *Service) setEnabled(ctx context.Context, id string, enabled bool) (*Monitor, error) {
	m, err := s.monitors.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if m.Enabled == enabled {
		return m, nil
	}
	now := time.Now().UTC()
	m.Enabled = enabled
	m.UpdatedAt = now
	if err := s.monitors.Update(ctx, m); err != nil {
		return nil, err
	}

	st, err := s.states.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	st.State = StateUnknown
	if !enabled {
		st.State = StatePaused
	}
	st.UpdatedAt = now
	if err := s.states.Upsert(ctx, st); err != nil {
		return nil, err
	}

	eventType, kind := EventMonitorEnabled, ChangeEnabled
	if !enabled {
		eventType, kind = EventMonitorDisabled, ChangeDisabled
	}
	if err := s.recordEvent(ctx, eventType, id, now); err != nil {
		return nil, err
	}
	s.notify(kind, m)
	return m, nil
}

// recordEvent appends a monitor-scoped event to the audit log.
func (s *Service) recordEvent(ctx context.Context, eventType, monitorID string, at time.Time) error {
	id := monitorID
	return s.events.Insert(ctx, &Event{
		ID:        NewID(),
		Type:      eventType,
		MonitorID: &id,
		CreatedAt: at,
	})
}

// notify delivers a Change to the OnChange observer if one is registered.
func (s *Service) notify(kind ChangeKind, m *Monitor) {
	if s.OnChange != nil {
		s.OnChange(Change{Kind: kind, Monitor: m})
	}
}

// validateInput checks a monitor's type-agnostic fields and its HTTP config.
func validateInput(m *Monitor) error {
	if err := ValidateMonitor(m); err != nil {
		return err
	}
	// ValidateMonitor guarantees the type is HTTP, the only MVP monitor type.
	var cfg HTTPMonitorConfig
	if err := json.Unmarshal(m.Config, &cfg); err != nil {
		return &FieldError{Field: "config", Message: "must be valid JSON"}
	}
	return ValidateHTTPConfig(&cfg)
}
