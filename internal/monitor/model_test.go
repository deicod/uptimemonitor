package monitor

import (
	"sort"
	"sync"
	"testing"
)

// State constants are persisted in SQLite and exchanged over IPC, so their
// string values are part of the wire/storage contract and must not drift.
func TestMonitorStateValues(t *testing.T) {
	cases := map[MonitorState]string{
		StateUnknown: "unknown",
		StateUp:      "up",
		StateDown:    "down",
		StatePaused:  "paused",
	}
	for state, want := range cases {
		if string(state) != want {
			t.Errorf("state = %q, want %q", state, want)
		}
	}
}

// MonitorTypeHTTP is matched against stored/wire values, so its literal matters.
func TestMonitorTypeValue(t *testing.T) {
	if MonitorTypeHTTP != "http" {
		t.Errorf("MonitorTypeHTTP = %q, want %q", MonitorTypeHTTP, "http")
	}
}

// NewID must yield unique IDs even under concurrent use, since records across
// goroutines (e.g. scheduler workers) rely on IDs as primary keys.
func TestNewIDUnique(t *testing.T) {
	const n = 1000
	var mu sync.Mutex
	seen := make(map[string]struct{}, n)
	var wg sync.WaitGroup
	for range n {
		wg.Go(func() {
			id := NewID()
			mu.Lock()
			seen[id] = struct{}{}
			mu.Unlock()
		})
	}
	wg.Wait()
	if len(seen) != n {
		t.Errorf("got %d unique IDs, want %d", len(seen), n)
	}
}

// IDs must sort lexically into creation order so that ordering rows by ID is
// equivalent to ordering them chronologically (SPEC §6 decision 1).
func TestNewIDLexicallySortable(t *testing.T) {
	const n = 100
	ids := make([]string, n)
	for i := range ids {
		ids[i] = NewID()
	}
	sorted := append([]string(nil), ids...)
	sort.Strings(sorted)
	for i := range ids {
		if ids[i] != sorted[i] {
			t.Fatalf("IDs are not in lexical creation order at index %d", i)
		}
	}
}
