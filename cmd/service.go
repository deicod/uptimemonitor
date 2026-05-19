/*
Copyright © 2026 Darko Luketic <info@icod.de>
*/
package cmd

import (
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/deicod/uptimemonitor/internal/app"
)

// serviceCmd represents the service command.
var serviceCmd = &cobra.Command{
	Use:   "service",
	Short: "Run the Uptime Monitor service",
	Long: `Run the long-lived Uptime Monitor service.

The service owns persistence, scheduling, probe execution, monitor state,
incidents, notification delivery, and the local IPC server that the TUI
connects to.`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		// SIGINT/SIGTERM cancel the context, driving graceful shutdown
		// (SPEC §9.3). The signal handler is removed when the command returns.
		ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
		defer stop()
		return app.Run(ctx, cfg)
	},
}

func init() {
	rootCmd.AddCommand(serviceCmd)
}
