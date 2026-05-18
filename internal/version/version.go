// Package version exposes build-time version metadata for the uptimemonitor
// binary.
package version

import "fmt"

// Build metadata. These default to "dev" for non-release builds and are
// overridden at link time, e.g.:
//
//	go build -ldflags "-X github.com/deicod/uptimemonitor/internal/version.Version=1.2.3"
var (
	// Version is the released semantic version, or "dev".
	Version = "dev"
	// Commit is the git commit the binary was built from, or "dev".
	Commit = "dev"
	// Date is the build timestamp, or "dev".
	Date = "dev"
)

// String returns a human-readable, single-line version summary.
func String() string {
	return fmt.Sprintf("%s (commit %s, built %s)", Version, Commit, Date)
}
