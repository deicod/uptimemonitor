// Package tsdb owns uptimemonitor's time-series storage (SPEC §14). It wraps a
// Prometheus TSDB database, configures retention from the service config, and
// exposes appender/querier accessors for the probe-result pipeline. No other
// package should open the TSDB directory directly.
package tsdb

import (
	"context"
	"fmt"
	"time"

	"github.com/prometheus/prometheus/storage"
	"github.com/prometheus/prometheus/tsdb"
)

// Store wraps a Prometheus TSDB instance for the service's time-series data.
type Store struct {
	db *tsdb.DB
}

// Open opens (creating if absent) the Prometheus TSDB at dir with the given
// retention duration. The retention is converted to milliseconds as expected by
// the TSDB options. A nil prometheus.Registerer and nil stats are used because
// this is an embedded use — no Prometheus metrics export in MVP (SPEC §3).
func Open(dir string, retention time.Duration) (*Store, error) {
	if dir == "" {
		return nil, fmt.Errorf("tsdb: directory path is empty")
	}

	opts := tsdb.DefaultOptions()
	opts.RetentionDuration = retention.Milliseconds()

	// NoLockfile is intentionally left false so that concurrent opens of the
	// same directory fail fast.

	db, err := tsdb.Open(dir, nil, nil, opts, nil)
	if err != nil {
		return nil, fmt.Errorf("tsdb: open %s: %w", dir, err)
	}
	return &Store{db: db}, nil
}

// Appender returns a storage.Appender for writing time-series samples. Callers
// must Commit or Rollback the returned appender.
func (s *Store) Appender(ctx context.Context) storage.Appender {
	return s.db.Appender(ctx)
}

// Querier returns a storage.Querier for reading time-series samples within the
// [mint, maxt] millisecond timestamp range. Callers must Close the returned
// querier when done.
func (s *Store) Querier(mint, maxt int64) (storage.Querier, error) {
	return s.db.Querier(mint, maxt)
}

// Close flushes pending data and closes the TSDB.
func (s *Store) Close() error {
	return s.db.Close()
}
