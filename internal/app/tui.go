package app

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/deicod/uptimemonitor/internal/config"
	"github.com/deicod/uptimemonitor/internal/ipc"
	"github.com/deicod/uptimemonitor/internal/tui"
)

// RunTUI is the TUI process entry point (SPEC §9.2). The TUI is purely a
// client: it builds an IPC client for the configured Unix socket, verifies the
// service is reachable, and then launches the Bubble Tea UI.
//
// The reachability precheck runs before the UI takes over the terminal so that
// a stopped service produces a readable error the cmd layer can turn into a
// non-zero exit, rather than a broken full-screen UI.
func RunTUI(ctx context.Context, cfg *config.Config, in io.Reader, out io.Writer) error {
	client := ipc.NewClient(cfg.SocketPath)

	if _, err := client.Status(ctx); err != nil {
		var connErr *ipc.ConnectionError
		if errors.As(err, &connErr) {
			return fmt.Errorf("the uptimemonitor service does not appear to be running: %w", connErr)
		}
		return fmt.Errorf("failed to read service status: %w", err)
	}

	return tui.Run(ctx, client, in, out)
}
