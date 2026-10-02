// Package chaos holds each guild's /chaos window: a weekly schedule inside which the loop rolls
// against a higher chance than /chance. Mirrored in memory like the opt-outs, because the loop
// reads it for every guild on every tick.
package chaos

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/disgoorg/snowflake/v2"

	"github.com/be-sandaa/coucou/internal/schedule"
	"github.com/be-sandaa/coucou/internal/store"
)

type Window struct {
	schedule.Window
	Chance int
}

type Store struct {
	db store.Store
	mu sync.RWMutex
	m  map[snowflake.ID]Window
}

func New(db store.Store) *Store { return &Store{db: db, m: map[snowflake.ID]Window{}} }

func parse(c store.Chaos, now time.Time) (Window, error) {
	w, err := schedule.Parse(c.Rule, time.Duration(c.Hours)*time.Hour, now)
	return Window{Window: w, Chance: c.Chance}, err
}

func (s *Store) Load(ctx context.Context) error {
	rows, err := s.db.ListChaos(ctx)
	if err != nil {
		return err
	}
	now := time.Now()
	m := make(map[snowflake.ID]Window, len(rows))
	for _, r := range rows {
		w, err := parse(r, now)
		if err != nil {
			// Dropped rather than held open, unlike an opt-out: the safe side of a window nobody can
			// read is the usual odds.
			slog.Error("chaos: unreadable rule, ignoring the window", slog.Any("guild", r.Guild), slog.Any("err", err))
			continue
		}
		m[r.Guild] = w
	}
	s.mu.Lock()
	s.m = m
	s.mu.Unlock()
	return nil
}

func (s *Store) Get(guild snowflake.ID) (Window, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	w, ok := s.m[guild]
	return w, ok
}

// Chance is the chance the loop rolls against: base, or the window's chance while it is on and
// higher. A /chance raised past the window after it was set wins, so chaos never lowers the odds.
func (s *Store) Chance(guild snowflake.ID, base int, now time.Time) int {
	if w, ok := s.Get(guild); ok && w.Active(now) {
		return max(base, w.Chance)
	}
	return base
}

// Set appends the row, then mirrors it; an empty Rule is /chaos off.
func (s *Store) Set(ctx context.Context, c store.Chaos) error {
	var w Window
	if c.Rule != "" {
		var err error
		if w, err = parse(c, time.Now()); err != nil {
			return err
		}
	}
	if err := s.db.AppendChaos(ctx, c); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if c.Rule == "" {
		delete(s.m, c.Guild)
	} else {
		s.m[c.Guild] = w
	}
	return nil
}

// Run keeps the cached windows current, as optout.Store.Run does.
func (s *Store) Run(ctx context.Context) error {
	t := time.NewTicker(schedule.Refresh)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
		s.refresh(time.Now())
	}
}

func (s *Store) refresh(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for g, w := range s.m {
		w.At(now)
		s.m[g] = w
	}
}
