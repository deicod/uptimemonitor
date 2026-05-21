package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/deicod/uptimemonitor/internal/ipc"
)

// stubClient is a fake tui.Client used to test IPC commands without a running
// service (SPEC §19.3, §24). Status, ListMonitors, GetMonitor and the
// monitor-scoped incident/event readers carry behaviour; the remaining methods
// exist solely to satisfy the Client interface.
type stubClient struct {
	status     ipc.StatusResponse
	statusErr  error
	monitors   []ipc.MonitorResponse
	monitor    ipc.MonitorResponse
	incidents  []ipc.IncidentResponse
	events     []ipc.EventResponse
	checks     []ipc.CheckResultResponse
	run        ipc.RunMonitorResponse
	history    ipc.HistoryResponse
	historyErr error
}

func (c stubClient) Status(context.Context) (ipc.StatusResponse, error) {
	return c.status, c.statusErr
}

func (c stubClient) ListMonitors(context.Context, ipc.MonitorListFilter) ([]ipc.MonitorResponse, error) {
	return c.monitors, nil
}

func (stubClient) CreateMonitor(context.Context, ipc.CreateMonitorRequest) (ipc.MonitorResponse, error) {
	return ipc.MonitorResponse{}, nil
}

func (c stubClient) GetMonitor(context.Context, string) (ipc.MonitorResponse, error) {
	return c.monitor, nil
}

func (stubClient) UpdateMonitor(context.Context, string, ipc.UpdateMonitorRequest) (ipc.MonitorResponse, error) {
	return ipc.MonitorResponse{}, nil
}

func (stubClient) DeleteMonitor(context.Context, string) error { return nil }

func (stubClient) ListIncidents(context.Context) ([]ipc.IncidentResponse, error) { return nil, nil }

func (c stubClient) ListMonitorIncidents(context.Context, string) ([]ipc.IncidentResponse, error) {
	return c.incidents, nil
}

func (stubClient) ListEvents(context.Context) ([]ipc.EventResponse, error) { return nil, nil }

func (c stubClient) ListMonitorEvents(context.Context, string) ([]ipc.EventResponse, error) {
	return c.events, nil
}

func (c stubClient) RunMonitor(context.Context, string) (ipc.RunMonitorResponse, error) {
	return c.run, nil
}

func (c stubClient) RecentChecks(context.Context, string, int) ([]ipc.CheckResultResponse, error) {
	return c.checks, nil
}

func (c stubClient) History(context.Context, string, string) (ipc.HistoryResponse, error) {
	return c.history, c.historyErr
}

// TestIPCCmdSuccess verifies the async IPC command pattern (SPEC §19.3): a
// successful call yields the message produced by the ok callback, so screens
// can react to loaded data.
func TestIPCCmdSuccess(t *testing.T) {
	c := stubClient{status: ipc.StatusResponse{Version: "1.2.3", State: "ready"}}

	cmd := ipcCmd(c.Status, func(s ipc.StatusResponse) tea.Msg {
		return homeStatusLoadedMsg{status: s}
	})
	msg := cmd()

	loaded, ok := msg.(homeStatusLoadedMsg)
	if !ok {
		t.Fatalf("expected homeStatusLoadedMsg, got %T", msg)
	}
	if loaded.status.Version != "1.2.3" {
		t.Errorf("status not propagated through the command: %+v", loaded.status)
	}
}

// TestIPCCmdError verifies that a failing IPC call is turned into an errMsg so
// the root model can route it to the error dialog rather than the failure
// being silently lost (SPEC §19.3, §19.4).
func TestIPCCmdError(t *testing.T) {
	c := stubClient{statusErr: errors.New("service down")}

	cmd := ipcCmd(c.Status, func(s ipc.StatusResponse) tea.Msg {
		return homeStatusLoadedMsg{status: s}
	})
	msg := cmd()

	em, ok := msg.(errMsg)
	if !ok {
		t.Fatalf("expected errMsg on failure, got %T", msg)
	}
	if em.err == nil || !strings.Contains(em.err.Error(), "service down") {
		t.Errorf("errMsg does not carry the underlying error: %v", em.err)
	}
}

// TestHomeScreenLoadsStatus verifies the home screen fetches the service
// status on Init and stores it, so the operator can confirm the TUI connected.
func TestHomeScreenLoadsStatus(t *testing.T) {
	want := ipc.StatusResponse{Version: "9.9.9", State: "ready"}
	s := newHomeScreen(stubClient{status: want})

	cmd := s.Init()
	if cmd == nil {
		t.Fatal("home screen Init returned no command")
	}

	scr, _ := s.Update(cmd())
	hs, ok := scr.(*homeScreen)
	if !ok {
		t.Fatalf("home Update returned %T, want *homeScreen", scr)
	}
	if hs.status == nil || hs.status.Version != "9.9.9" {
		t.Fatalf("home screen did not store the fetched status: %+v", hs.status)
	}
	if !strings.Contains(hs.View(), "9.9.9") {
		t.Errorf("home view does not render the status:\n%s", hs.View())
	}
}
