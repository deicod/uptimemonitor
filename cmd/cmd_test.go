/*
Copyright © 2026 Darko Luketic <info@icod.de>
*/
package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/deicod/uptimemonitor/internal/version"
)

// execute runs the root command with the given args, capturing its output.
//
// The root command is a shared package-level value, so flag state from an
// earlier run (e.g. --help) would otherwise leak into the next. resetFlags
// restores every flag to its default before each run, keeping tests
// order-independent.
func execute(t *testing.T, args ...string) (string, error) {
	t.Helper()

	resetFlags(rootCmd)

	buf := &bytes.Buffer{}
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs(args)
	t.Cleanup(func() {
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
		rootCmd.SetArgs(nil)
	})

	err := rootCmd.Execute()
	return buf.String(), err
}

// resetFlags restores every flag of cmd and its subcommands to its default
// value so a prior Execute call cannot influence the next.
func resetFlags(cmd *cobra.Command) {
	cmd.Flags().VisitAll(func(f *pflag.Flag) {
		_ = f.Value.Set(f.DefValue)
		f.Changed = false
	})
	for _, sub := range cmd.Commands() {
		resetFlags(sub)
	}
}

func TestHelpListsSubcommands(t *testing.T) {
	out, err := execute(t, "--help")
	if err != nil {
		t.Fatalf("--help returned error: %v", err)
	}
	for _, want := range []string{"service", "tui"} {
		if !strings.Contains(out, want) {
			t.Errorf("--help output does not list %q subcommand:\n%s", want, out)
		}
	}
	if strings.Contains(out, "A brief description") {
		t.Errorf("--help still contains placeholder scaffold text:\n%s", out)
	}
}

func TestNoToggleFlag(t *testing.T) {
	if rootCmd.Flags().Lookup("toggle") != nil {
		t.Error("generated --toggle flag should have been removed")
	}
}

func TestVersionFlag(t *testing.T) {
	out, err := execute(t, "--version")
	if err != nil {
		t.Fatalf("--version returned error: %v", err)
	}
	if !strings.Contains(out, version.String()) {
		t.Errorf("--version output %q does not contain %q", out, version.String())
	}
}

func TestServiceCommandRuns(t *testing.T) {
	if _, err := execute(t, "service"); err != nil {
		t.Fatalf("service command returned error: %v", err)
	}
}

func TestTUICommandRuns(t *testing.T) {
	if _, err := execute(t, "tui"); err != nil {
		t.Fatalf("tui command returned error: %v", err)
	}
}
