// Package systemd implements the sd_notify protocol so the uptimemonitor
// service can report readiness and liveness to systemd (SPEC §21.2–21.3).
//
// Every operation is a no-op when the process is not run under systemd, i.e.
// when the NOTIFY_SOCKET environment variable is unset. This lets the same
// code run unchanged under systemd, in containers, and during development.
package systemd

import (
	"context"
	"log/slog"
	"net"
	"os"
	"strconv"
	"time"
)

// Notify sends a single sd_notify status line to systemd over the datagram
// socket named by NOTIFY_SOCKET. The sent result reports whether a message was
// actually written; it is false (with a nil error) when NOTIFY_SOCKET is unset,
// meaning the process is not running under systemd.
func Notify(state string) (sent bool, err error) {
	addr := os.Getenv("NOTIFY_SOCKET")
	if addr == "" {
		return false, nil
	}

	// systemd uses the abstract namespace when the path starts with '@'.
	name := addr
	if name[0] == '@' {
		name = "\x00" + name[1:]
	}

	conn, err := net.DialUnix("unixgram", nil, &net.UnixAddr{Name: name, Net: "unixgram"})
	if err != nil {
		return false, err
	}
	defer conn.Close()

	if _, err := conn.Write([]byte(state)); err != nil {
		return false, err
	}
	return true, nil
}

// Ready notifies systemd that startup is complete (SPEC §21.2). It must be
// called only after config is loaded, SQLite is migrated, the TSDB is open,
// the IPC server is listening, and the scheduler is running.
func Ready() (bool, error) {
	return Notify("READY=1")
}

// WatchdogInterval reports the watchdog ping interval requested by systemd via
// WATCHDOG_USEC. The bool result is false when the watchdog is not enabled for
// this process — either WATCHDOG_USEC is unset/invalid, or WATCHDOG_PID names a
// different process. The returned duration is the time between pings systemd
// expects; callers should ping at most half that often.
func WatchdogInterval() (time.Duration, bool) {
	usec := os.Getenv("WATCHDOG_USEC")
	if usec == "" {
		return 0, false
	}
	micros, err := strconv.ParseInt(usec, 10, 64)
	if err != nil || micros <= 0 {
		return 0, false
	}

	// WATCHDOG_PID, when set, restricts watchdog handling to that exact PID so
	// a forked child does not accidentally keep the parent's watchdog alive.
	if pid := os.Getenv("WATCHDOG_PID"); pid != "" {
		if pid != strconv.Itoa(os.Getpid()) {
			return 0, false
		}
	}
	return time.Duration(micros) * time.Microsecond, true
}

// StartWatchdog runs a background pinger that sends WATCHDOG=1 to systemd until
// ctx is cancelled (SPEC §21.3). Pings are sent at half the interval systemd
// requested, leaving margin for scheduling jitter. It is a no-op — returning
// immediately — when the watchdog is not enabled for this process.
func StartWatchdog(ctx context.Context, logger *slog.Logger) {
	interval, ok := WatchdogInterval()
	if !ok {
		return
	}
	go pingWatchdog(ctx, interval/2, logger)
}

// pingWatchdog sends WATCHDOG=1 every period until ctx is cancelled.
func pingWatchdog(ctx context.Context, period time.Duration, logger *slog.Logger) {
	ticker := time.NewTicker(period)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := Notify("WATCHDOG=1"); err != nil && logger != nil {
				logger.Warn("systemd watchdog ping failed", "error", err.Error())
			}
		}
	}
}
