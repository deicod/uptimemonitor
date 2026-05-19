/*
Copyright © 2026 Darko Luketic <info@icod.de>
*/
package cmd

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

// serviceConfig writes a config.yaml rooted under a temp directory and returns
// the config-file path and the socket path it declares. Using temp paths keeps
// the service-command tests from touching real system directories.
func serviceConfig(t *testing.T) (cfgPath, socketPath string) {
	t.Helper()
	dir := t.TempDir()
	data := filepath.Join(dir, "data")
	run := filepath.Join(dir, "run")
	socketPath = filepath.Join(run, "um.sock")
	content := "data_dir: " + data + "\n" +
		"runtime_dir: " + run + "\n" +
		"sqlite_path: " + filepath.Join(data, "config.db") + "\n" +
		"tsdb_path: " + filepath.Join(data, "tsdb") + "\n" +
		"socket_path: " + socketPath + "\n" +
		"log_level: error\n"
	cfgPath = writeConfig(t, content)
	return cfgPath, socketPath
}

// runService runs `uptimemonitor service` with cfgPath in a goroutine, waits
// for it to bind socketPath, then cancels its context to drive shutdown. It
// returns the command's exit error. The shared rootCmd is safe here because
// tests in this package run sequentially.
func runService(t *testing.T, cfgPath, socketPath string) error {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	resetFlags(rootCmd)
	rootCmd.SetArgs([]string{"service", "--config", cfgPath})
	t.Cleanup(func() { rootCmd.SetArgs(nil) })

	errCh := make(chan error, 1)
	go func() { errCh <- rootCmd.ExecuteContext(ctx) }()

	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(socketPath); err == nil {
			break
		}
		select {
		case err := <-errCh:
			t.Fatalf("service exited before binding its socket: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("service did not bind its socket within timeout")
		}
		time.Sleep(10 * time.Millisecond)
	}

	cancel() // simulate SIGTERM

	select {
	case err := <-errCh:
		return err
	case <-time.After(10 * time.Second):
		t.Fatal("service did not exit after shutdown signal")
		return nil
	}
}

// TestServiceCommandRuns verifies the service subcommand starts the real
// service and exits cleanly on a shutdown signal.
func TestServiceCommandRuns(t *testing.T) {
	cfgPath, socketPath := serviceConfig(t)
	if err := runService(t, cfgPath, socketPath); err != nil {
		t.Fatalf("service command returned error: %v", err)
	}
}

func TestTUICommandRuns(t *testing.T) {
	if _, err := execute(t, "tui"); err != nil {
		t.Fatalf("tui command returned error: %v", err)
	}
}

// writeConfig writes content to a temp config.yaml and returns its path.
func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

// TestInvalidConfigFailsFast verifies a config that violates a SPEC §8.5 rule
// stops the command before its Run with a field-aware, readable error — the
// operator must learn exactly which key to fix.
func TestInvalidConfigFailsFast(t *testing.T) {
	path := writeConfig(t, "service:\n  check_workers: 0\n")
	_, err := execute(t, "service", "--config", path)
	if err == nil {
		t.Fatal("expected non-nil error for invalid config, got nil")
	}
	if !strings.Contains(err.Error(), "check_workers") {
		t.Errorf("error %q does not name the offending field", err.Error())
	}
}
