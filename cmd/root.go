/*
Copyright © 2026 Darko Luketic <info@icod.de>

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in
all copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
THE SOFTWARE.
*/
package cmd

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/deicod/uptimemonitor/internal/config"
	"github.com/deicod/uptimemonitor/internal/version"
)

// cfg holds the configuration loaded and validated in PersistentPreRunE. It is
// populated before any subcommand's Run executes and read by service/tui.
var cfg *config.Config

// rootCmd represents the base command when called without any subcommands.
var rootCmd = &cobra.Command{
	Use:   "uptimemonitor",
	Short: "Self-hosted uptime monitoring with a terminal UI",
	Long: `Uptime Monitor is a self-hosted service that periodically probes HTTP
endpoints, tracks their state and incidents, and delivers notifications.

It runs as a long-lived service that owns persistence, scheduling, and
notification delivery, and ships with a Bubble Tea terminal UI client that
manages monitors over a local Unix socket.`,
	Version: version.String(),
	// A config load/validation failure is not a usage error; silence the
	// usage dump so the field-aware error stands on its own.
	SilenceUsage: true,
	PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
		loaded, err := config.Load(cmd.Flags())
		if err != nil {
			return err
		}
		if err := config.Validate(loaded); err != nil {
			return err
		}
		cfg = loaded
		return nil
	},
}

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	err := rootCmd.Execute()
	if err != nil {
		os.Exit(1)
	}
}

func init() {
	// Persistent flags shared by all subcommands. --config selects the config
	// file; the rest override individual config keys (config.Load binds them).
	pf := rootCmd.PersistentFlags()
	pf.String("config", "", "path to config file")
	pf.String("log-level", "", "log level: debug, info, warn, error")
	pf.String("socket-path", "", "path to the IPC Unix socket")
	pf.String("data-dir", "", "path to the data directory")
}
