/*
Copyright © 2026 Darko Luketic <info@icod.de>
*/
package cmd

import (
	"bytes"
	"strings"
	"testing"
)

// execute runs the root command with the given args, capturing its output.
func execute(t *testing.T, args ...string) (string, error) {
	t.Helper()

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
