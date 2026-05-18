/*
Copyright © 2026 Darko Luketic <info@icod.de>
*/
package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

// serviceCmd represents the service command.
var serviceCmd = &cobra.Command{
	Use:   "service",
	Short: "Run the Uptime Monitor service",
	Long: `Run the long-lived Uptime Monitor service.

The service owns persistence, scheduling, probe execution, monitor state,
incidents, notification delivery, and the local IPC server that the TUI
connects to.`,
	Run: func(cmd *cobra.Command, args []string) {
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), "service called")
	},
}

func init() {
	rootCmd.AddCommand(serviceCmd)
}
