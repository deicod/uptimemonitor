package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/deicod/uptimemonitor/internal/ipc"
)

// fullStatus is a representative /v1/status response covering every field the
// status screen renders (SPEC §10.5).
func fullStatus() ipc.StatusResponse {
	return ipc.StatusResponse{
		Version:   "9.9.9",
		State:     "ready",
		StartedAt: time.Now().Add(-90 * time.Minute),
		SQLite:    ipc.StoreHealth{OK: true},
		TSDB:      ipc.StoreHealth{OK: false},
		Scheduler: ipc.SchedulerStatus{Running: true, Workers: 16},
		Monitors:  ipc.MonitorCounts{Total: 3, Active: 2},
	}
}

// TestStatusScreenLoadsStatus verifies the status screen fetches the service
// status on Init and stores it, so the operator sees live service health.
func TestStatusScreenLoadsStatus(t *testing.T) {
	s := newStatusScreen(stubClient{status: fullStatus()})

	cmd := s.Init()
	if cmd == nil {
		t.Fatal("status screen Init returned no command")
	}

	scr, _ := s.Update(cmd())
	ss, ok := scr.(*statusScreen)
	if !ok {
		t.Fatalf("status Update returned %T, want *statusScreen", scr)
	}
	if ss.status == nil || ss.status.Version != "9.9.9" {
		t.Fatalf("status screen did not store the fetched status: %+v", ss.status)
	}
}

// TestStatusScreenViewRendersFields verifies the View renders every SPEC §10.5
// status field, including the per-store health labels, so the screen actually
// communicates service state rather than a subset of it.
func TestStatusScreenViewRendersFields(t *testing.T) {
	s := newStatusScreen(stubClient{})
	s.Update(statusLoadedMsg{status: fullStatus()})

	view := s.View()
	for _, want := range []string{
		"9.9.9", "ready",
		"sqlite", "ok",
		"tsdb", "unhealthy",
		"workers=16",
		"total=3", "active=2",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("status view missing %q:\n%s", want, view)
		}
	}
}

// TestStatusScreenViewBeforeLoad verifies the screen renders a connecting
// placeholder before the first response arrives instead of a blank panel.
func TestStatusScreenViewBeforeLoad(t *testing.T) {
	s := newStatusScreen(stubClient{})
	if !strings.Contains(s.View(), "connecting") {
		t.Errorf("status view before load does not show a placeholder:\n%s", s.View())
	}
}

// TestStatusScreenRefreshKey verifies the refresh key re-fetches the status so
// the operator can update a stale view without leaving the screen.
func TestStatusScreenRefreshKey(t *testing.T) {
	s := newStatusScreen(stubClient{status: fullStatus()})
	_, cmd := s.Update(runeKey('r'))
	if cmd == nil {
		t.Fatal("refresh key produced no command")
	}
	if _, ok := cmd().(statusLoadedMsg); !ok {
		t.Fatalf("refresh key did not re-fetch the status, got %T", cmd())
	}
}

// TestHomeNavigatesToStatus verifies the home screen opens the status screen on
// its navigation key, so the dedicated screen is reachable by the operator.
func TestHomeNavigatesToStatus(t *testing.T) {
	s := newHomeScreen(stubClient{})
	_, cmd := s.Update(runeKey('s'))
	if cmd == nil {
		t.Fatal("status navigation key produced no command")
	}
	if _, ok := cmd().(pushScreenMsg); !ok {
		t.Fatalf("home did not push a screen, got %T", cmd())
	}
}
