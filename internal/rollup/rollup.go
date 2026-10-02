// Package rollup keeps the store's rolled-up series fresh. The series read exact without it — a
// backend that rolls up also counts whatever its rollup has not seen yet, live — so the timer only
// decides how much of each /stats is counted on read, never what it says.
package rollup

import (
	"context"
	"log/slog"
	"time"

	"github.com/be-sandaa/coucou/internal/store"
)

// Every is how long the live side of a series gets to grow between refreshes: a quarter of an hour
// of plays is nothing to count on read, and the recount it saves is a month of them.
const Every = 15 * time.Minute

type Refresher struct{ db store.Store }

func New(db store.Store) *Refresher { return &Refresher{db: db} }

// Run refreshes at once and then on a timer. At once, because the rollup is as old as the last
// process that refreshed it, and a restart after a long outage would otherwise count all of that
// on read for the first quarter of an hour.
func (r *Refresher) Run(ctx context.Context) error {
	t := time.NewTicker(Every)
	defer t.Stop()
	for {
		r.refresh(ctx)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
	}
}

// refresh logs rather than fails: a stale rollup costs a slower /stats, not a wrong one.
func (r *Refresher) refresh(ctx context.Context) {
	start := time.Now()
	if err := r.db.RefreshAnalytics(ctx); err != nil {
		if ctx.Err() == nil {
			slog.Error("rollup: refreshing", slog.Any("err", err))
		}
		return
	}
	slog.Debug("rollup: refreshed", slog.Duration("took", time.Since(start)))
}
