// Package ranks keeps the percentile cut-points that a single row is ranked against when its
// population is every user or every guild. Only the cut-points live in memory, a few KB at any
// scale, so a request counts its own row and never the population.
package ranks

import (
	"context"
	"log/slog"
	"maps"
	"sort"
	"sync"
	"time"

	"github.com/be-sandaa/coucou/internal/store"
)

var metrics = []string{store.MetricHeard, store.MetricTriggered, store.MetricFled, store.MetricPlays, store.MetricListeners}

// Window is how far back every rank looks; the totals beside it stay all-time.
const Window = 30 * 24 * time.Hour

const refreshEvery = 15 * time.Minute

type Cuts struct {
	db store.Store
	mu sync.RWMutex
	m  map[string][]float64
}

func New(db store.Store) *Cuts { return &Cuts{db: db, m: map[string][]float64{}} }

// Run refreshes at once and then on a timer, so ranks are there from startup rather than after the
// first 15 minutes.
func (c *Cuts) Run(ctx context.Context) error {
	t := time.NewTicker(refreshEvery)
	defer t.Stop()
	for {
		c.refresh(ctx, time.Now())
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
	}
}

// refresh keeps a metric's previous cut-points when its query fails: a stale rank beats none.
func (c *Cuts) refresh(ctx context.Context, now time.Time) {
	c.mu.RLock()
	m := maps.Clone(c.m)
	c.mu.RUnlock()
	for _, metric := range metrics {
		cuts, err := c.db.Cuts(ctx, metric, now.Add(-Window), now)
		if err != nil {
			slog.Error("ranks: refreshing cut-points", slog.String("metric", metric), slog.Any("err", err))
			continue
		}
		m[metric] = cuts
	}
	c.mu.Lock()
	c.m = m
	c.mu.Unlock()
}

// Below is the whole percent of metric's population strictly below v, false while there are no
// cut-points to compare against.
func (c *Cuts) Below(metric string, v float64) (int, bool) {
	c.mu.RLock()
	cuts := c.m[metric]
	c.mu.RUnlock()
	if len(cuts) == 0 {
		return 0, false
	}
	return below(cuts, v), true
}

// below counts the cut-points under v. Cut k is the smallest value at least k% of the population is
// at or under, so at least k% sit strictly below v exactly when cut k < v.
func below(cuts []float64, v float64) int {
	return sort.SearchFloat64s(cuts, v) * 100 / len(cuts)
}
