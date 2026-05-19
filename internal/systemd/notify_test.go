package systemd

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// fakeNotifySocket binds a unixgram socket at a temp path, points
// NOTIFY_SOCKET at it, and returns the bound connection for reading payloads.
func fakeNotifySocket(t *testing.T) *net.UnixConn {
	t.Helper()
	// The path must stay short: Unix socket paths are length-limited.
	dir, err := os.MkdirTemp("", "sdnotify")
	if err != nil {
		t.Fatalf("mkdir temp: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })

	path := filepath.Join(dir, "notify.sock")
	conn, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: path, Net: "unixgram"})
	if err != nil {
		t.Fatalf("listen unixgram: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	t.Setenv("NOTIFY_SOCKET", path)
	return conn
}

// readPayload reads one datagram, failing if none arrives before the deadline.
func readPayload(t *testing.T, conn *net.UnixConn) string {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	buf := make([]byte, 256)
	n, _, err := conn.ReadFromUnix(buf)
	if err != nil {
		t.Fatalf("read payload: %v", err)
	}
	return string(buf[:n])
}

func TestNotifyNoOpsWithoutNotifySocket(t *testing.T) {
	// NOTIFY_SOCKET is what tells the process it runs under systemd; with it
	// unset, every notify call must do nothing and report sent=false so the
	// caller can run identically outside systemd.
	t.Setenv("NOTIFY_SOCKET", "")

	sent, err := Notify("READY=1")
	if err != nil {
		t.Fatalf("Notify returned error: %v", err)
	}
	if sent {
		t.Fatal("Notify reported sent=true with NOTIFY_SOCKET unset")
	}
}

func TestNotifyWritesPayload(t *testing.T) {
	conn := fakeNotifySocket(t)

	sent, err := Notify("WATCHDOG=1")
	if err != nil {
		t.Fatalf("Notify returned error: %v", err)
	}
	if !sent {
		t.Fatal("Notify reported sent=false with NOTIFY_SOCKET set")
	}
	if got := readPayload(t, conn); got != "WATCHDOG=1" {
		t.Fatalf("payload = %q, want %q", got, "WATCHDOG=1")
	}
}

func TestReadyWritesReadyPayload(t *testing.T) {
	// Ready must send exactly READY=1: systemd keys service activation off
	// that literal token, so a different payload would leave Type=notify
	// units stuck in "activating".
	conn := fakeNotifySocket(t)

	sent, err := Ready()
	if err != nil {
		t.Fatalf("Ready returned error: %v", err)
	}
	if !sent {
		t.Fatal("Ready reported sent=false with NOTIFY_SOCKET set")
	}
	if got := readPayload(t, conn); got != "READY=1" {
		t.Fatalf("payload = %q, want %q", got, "READY=1")
	}
}

func TestWatchdogIntervalUnsetIsDisabled(t *testing.T) {
	t.Setenv("WATCHDOG_USEC", "")

	if _, ok := WatchdogInterval(); ok {
		t.Fatal("WatchdogInterval reported enabled with WATCHDOG_USEC unset")
	}
}

func TestWatchdogIntervalParsesMicroseconds(t *testing.T) {
	t.Setenv("WATCHDOG_USEC", "30000000")
	t.Setenv("WATCHDOG_PID", "")

	d, ok := WatchdogInterval()
	if !ok {
		t.Fatal("WatchdogInterval reported disabled with a valid WATCHDOG_USEC")
	}
	if d != 30*time.Second {
		t.Fatalf("interval = %v, want %v", d, 30*time.Second)
	}
}

func TestWatchdogIntervalInvalidIsDisabled(t *testing.T) {
	t.Setenv("WATCHDOG_USEC", "not-a-number")

	if _, ok := WatchdogInterval(); ok {
		t.Fatal("WatchdogInterval reported enabled with an invalid WATCHDOG_USEC")
	}
}

func TestWatchdogIntervalForeignPIDIsDisabled(t *testing.T) {
	// WATCHDOG_PID restricts watchdog handling to one process; a value that is
	// not our PID means the watchdog was meant for a different process.
	t.Setenv("WATCHDOG_USEC", "30000000")
	t.Setenv("WATCHDOG_PID", strconv.Itoa(os.Getpid()+1))

	if _, ok := WatchdogInterval(); ok {
		t.Fatal("WatchdogInterval reported enabled for a foreign WATCHDOG_PID")
	}
}

func TestWatchdogIntervalMatchingPIDIsEnabled(t *testing.T) {
	t.Setenv("WATCHDOG_USEC", "30000000")
	t.Setenv("WATCHDOG_PID", strconv.Itoa(os.Getpid()))

	if _, ok := WatchdogInterval(); !ok {
		t.Fatal("WatchdogInterval reported disabled for our own WATCHDOG_PID")
	}
}

func TestStartWatchdogNoOpsWhenDisabled(t *testing.T) {
	// With the watchdog disabled, StartWatchdog must not ping; otherwise it
	// would dial a stale or absent NOTIFY_SOCKET on every tick.
	conn := fakeNotifySocket(t)
	t.Setenv("WATCHDOG_USEC", "")

	StartWatchdog(t.Context(), nil)

	if err := conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	buf := make([]byte, 64)
	if _, _, err := conn.ReadFromUnix(buf); err == nil {
		t.Fatal("StartWatchdog sent a ping while the watchdog was disabled")
	}
}

func TestStartWatchdogPings(t *testing.T) {
	conn := fakeNotifySocket(t)
	// 40ms interval → ping every 20ms, so a ping arrives well within the read
	// deadline below.
	t.Setenv("WATCHDOG_USEC", "40000")
	t.Setenv("WATCHDOG_PID", strconv.Itoa(os.Getpid()))

	StartWatchdog(t.Context(), nil)

	if got := readPayload(t, conn); got != "WATCHDOG=1" {
		t.Fatalf("payload = %q, want %q", got, "WATCHDOG=1")
	}
}
