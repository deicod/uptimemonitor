package app

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/deicod/uptimemonitor/internal/config"
	"github.com/deicod/uptimemonitor/internal/ipc"
)

// RunTUI is the TUI process entry point (SPEC §9.2). The TUI is purely a
// client: it builds an IPC client for the configured Unix socket, fetches the
// service status, and reports it.
//
// If the service is not running, RunTUI returns a readable error naming the
// socket so the cmd layer can exit non-zero with an operator-friendly message
// rather than a raw transport error.
//
// Bubble Tea is not wired yet; this connect-and-print behaviour is replaced by
// the full TUI in M6.
func RunTUI(ctx context.Context, cfg *config.Config, out io.Writer) error {
	client := ipc.NewClient(cfg.SocketPath)

	status, err := client.Status(ctx)
	if err != nil {
		var connErr *ipc.ConnectionError
		if errors.As(err, &connErr) {
			return fmt.Errorf("the uptimemonitor service does not appear to be running: %w", connErr)
		}
		return fmt.Errorf("failed to read service status: %w", err)
	}

	fmt.Fprintf(out, "uptimemonitor service\n")
	fmt.Fprintf(out, "  version:   %s\n", status.Version)
	fmt.Fprintf(out, "  state:     %s\n", status.State)
	fmt.Fprintf(out, "  started:   %s\n", status.StartedAt.Format("2006-01-02 15:04:05 MST"))
	fmt.Fprintf(out, "  sqlite:    %s\n", healthLabel(status.SQLite.OK))
	fmt.Fprintf(out, "  tsdb:      %s\n", healthLabel(status.TSDB.OK))
	fmt.Fprintf(out, "  scheduler: running=%t workers=%d\n", status.Scheduler.Running, status.Scheduler.Workers)
	fmt.Fprintf(out, "  monitors:  total=%d active=%d\n", status.Monitors.Total, status.Monitors.Active)
	return nil
}

// healthLabel renders a storage health flag as a word.
func healthLabel(ok bool) string {
	if ok {
		return "ok"
	}
	return "unhealthy"
}
