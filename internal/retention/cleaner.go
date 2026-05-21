// Package retention enforces uptimemonitor's storage retention by pruning old
// SQLite check_result rows and triggering TSDB compaction (SPEC §12.5, §14.4,
// §14.6).
//
// The Cleaner runs once at startup and then on a fixed interval (SPEC §14.6
// recommends 1h). Both storage backends are pruned in the same pass so that an
// operator inspecting either can rely on a single retention guarantee.
package retention

import (
	"context"
	"log/slog"
	"time"
)

// CheckResultPruner deletes check_results rows whose started_at is older than
// the supplied cutoff and returns the number of rows removed.
// *sqlite.CheckResultRepo satisfies this interface.
type CheckResultPruner interface {
	PruneOlderThan(ctx context.Context, cutoff time.Time) (int64, error)
}

// TSDBCleaner triggers TSDB compaction so the Prometheus retention applies.
// *tsdb.Store satisfies this interface via Cleanup.
type TSDBCleaner interface {
	Cleanup(ctx context.Context) error
}

// Options configures a Cleaner.
type Options struct {
	// CheckResultRetention is the maximum age of rows kept in
	// check_results (SPEC §12.5). Rows with started_at before
	// now-CheckResultRetention are deleted.
	CheckResultRetention time.Duration
	// Interval is the period between cleanup runs after the initial run
	// at startup (SPEC §14.6). A non-positive value disables periodic runs.
	Interval time.Duration
	// Now returns the current time. It exists so tests can pin the cutoff
	// instead of racing against the wall clock; production code leaves it nil.
	Now func() time.Time
}

// Cleaner runs retention on both storage backends.
type Cleaner struct {
	checks CheckResultPruner
	tsdb   TSDBCleaner
	opts   Options
	logger *slog.Logger
}

// New builds a Cleaner with the supplied dependencies and options. The logger
// must not be nil; callers typically derive it via logging.Component(base,
// "retention").
func New(checks CheckResultPruner, tsdb TSDBCleaner, opts Options, logger *slog.Logger) *Cleaner {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &Cleaner{checks: checks, tsdb: tsdb, opts: opts, logger: logger}
}

// Run performs one retention pass: SQLite check_results older than the
// configured retention are pruned, then TSDB compaction is triggered. Both
// steps are attempted even if the first fails so that an outage in one backend
// does not block cleanup of the other. Run reports the first error
// encountered; subsequent ones are logged.
func (c *Cleaner) Run(ctx context.Context) error {
	cutoff := c.opts.Now().Add(-c.opts.CheckResultRetention)

	var firstErr error
	deleted, err := c.checks.PruneOlderThan(ctx, cutoff)
	if err != nil {
		c.logger.Error("prune check_results", "error", err.Error())
		firstErr = err
	} else {
		c.logger.Debug("pruned check_results", "deleted", deleted, "cutoff", cutoff)
	}

	if err := c.tsdb.Cleanup(ctx); err != nil {
		c.logger.Error("tsdb compaction", "error", err.Error())
		if firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// Start runs an initial cleanup pass and then a pass every Options.Interval
// until ctx is cancelled. Start blocks until ctx is done, so callers run it in
// a goroutine. A non-positive Interval skips the periodic loop but still runs
// the initial pass — useful for tests and for one-shot invocations.
func (c *Cleaner) Start(ctx context.Context) {
	if err := c.Run(ctx); err != nil {
		// Already logged inside Run; surface at info so operators see startup
		// retention failed but the service still starts.
		c.logger.Info("initial retention pass returned error", "error", err.Error())
	}
	if c.opts.Interval <= 0 {
		return
	}
	ticker := time.NewTicker(c.opts.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := c.Run(ctx); err != nil {
				c.logger.Info("periodic retention pass returned error", "error", err.Error())
			}
		}
	}
}
