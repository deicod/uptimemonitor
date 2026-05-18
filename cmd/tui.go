/*
Copyright © 2026 Darko Luketic <info@icod.de>
*/
package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

// tuiCmd represents the tui command.
var tuiCmd = &cobra.Command{
	Use:   "tui",
	Short: "Launch the Uptime Monitor terminal UI",
	Long: `Launch the Uptime Monitor terminal UI.

The TUI is a client that connects to a running service over its local Unix
socket and manages monitors, incidents, and notification targets. All reads
and writes go through the service; the TUI never touches storage directly.`,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Fprintln(cmd.OutOrStdout(), "tui called")
	},
}

func init() {
	rootCmd.AddCommand(tuiCmd)
}
