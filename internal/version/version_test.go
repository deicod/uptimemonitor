package version

import (
	"strings"
	"testing"
)

func TestStringDefaults(t *testing.T) {
	if Version != "dev" || Commit != "dev" || Date != "dev" {
		t.Fatalf("expected default build vars to be %q; got Version=%q Commit=%q Date=%q",
			"dev", Version, Commit, Date)
	}
	if got := String(); !strings.Contains(got, "dev") {
		t.Errorf("String() = %q, want it to contain %q", got, "dev")
	}
}

func TestStringIncludesAllFields(t *testing.T) {
	origV, origC, origD := Version, Commit, Date
	t.Cleanup(func() { Version, Commit, Date = origV, origC, origD })

	Version, Commit, Date = "1.2.3", "abc1234", "2026-05-18"
	got := String()
	for _, want := range []string{"1.2.3", "abc1234", "2026-05-18"} {
		if !strings.Contains(got, want) {
			t.Errorf("String() = %q, want it to contain %q", got, want)
		}
	}
}
