package tui

import (
	"context"
	"errors"
	"fmt"
	"io"

	tea "charm.land/bubbletea/v2"

	"github.com/deicod/uptimemonitor/internal/ipc"
)

// Client is the subset of the IPC client the TUI uses. It is declared as an
// interface so screens can be exercised against a fake client in tests
// (SPEC §19.3, §24); *ipc.Client satisfies it.
type Client interface {
	Status(ctx context.Context) (ipc.StatusResponse, error)
	ListMonitors(ctx context.Context, filter ipc.MonitorListFilter) ([]ipc.MonitorResponse, error)
	CreateMonitor(ctx context.Context, req ipc.CreateMonitorRequest) (ipc.MonitorResponse, error)
	GetMonitor(ctx context.Context, id string) (ipc.MonitorResponse, error)
	UpdateMonitor(ctx context.Context, id string, req ipc.UpdateMonitorRequest) (ipc.MonitorResponse, error)
	DeleteMonitor(ctx context.Context, id string) error
	ListIncidents(ctx context.Context) ([]ipc.IncidentResponse, error)
	ListMonitorIncidents(ctx context.Context, monitorID string) ([]ipc.IncidentResponse, error)
	ListEvents(ctx context.Context) ([]ipc.EventResponse, error)
	ListMonitorEvents(ctx context.Context, monitorID string) ([]ipc.EventResponse, error)
	RunMonitor(ctx context.Context, monitorID string) (ipc.RunMonitorResponse, error)
	RecentChecks(ctx context.Context, monitorID string, limit int) ([]ipc.CheckResultResponse, error)
}

var _ Client = (*ipc.Client)(nil)

// Run starts the TUI Bubble Tea program and blocks until the user quits or ctx
// is cancelled. A cancelled context is a clean shutdown, not an error.
func Run(ctx context.Context, client Client, in io.Reader, out io.Writer) error {
	p := tea.NewProgram(
		newModel(client),
		tea.WithContext(ctx),
		tea.WithInput(in),
		tea.WithOutput(out),
	)
	if _, err := p.Run(); err != nil {
		if errors.Is(err, tea.ErrProgramKilled) || errors.Is(err, context.Canceled) {
			return nil
		}
		return fmt.Errorf("tui: %w", err)
	}
	return nil
}

// ipcCmd adapts an IPC call into a Bubble Tea command (SPEC §19.3). On success
// it returns the message produced by ok; on failure it returns errMsg, which
// the root model routes to the error dialog.
func ipcCmd[T any](call func(context.Context) (T, error), ok func(T) tea.Msg) tea.Cmd {
	return func() tea.Msg {
		res, err := call(context.Background())
		if err != nil {
			return errMsg{err: err}
		}
		return ok(res)
	}
}
