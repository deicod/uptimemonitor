/*
Copyright © 2026 Darko Luketic <info@icod.de>
*/
package cmd

import (
	"github.com/spf13/cobra"

	"github.com/deicod/uptimemonitor/internal/app"
)

// tuiCmd represents the tui command.
var tuiCmd = &cobra.Command{
	Use:   "tui",
	Short: "Launch the Uptime Monitor terminal UI",
	Long: `Launch the Uptime Monitor terminal UI.

The TUI is a client that connects to a running service over its local Unix
socket and manages monitors, incidents, and notification targets. All reads
and writes go through the service; the TUI never touches storage directly.`,
	// A failure to reach the service is reported as a readable error, not a
	// usage error; suppress the usage dump.
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return app.RunTUI(cmd.Context(), cfg, cmd.InOrStdin(), cmd.OutOrStdout())
	},
}

func init() {
	rootCmd.AddCommand(tuiCmd)
}
