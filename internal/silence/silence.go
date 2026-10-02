// Package silence holds the stretches the bot stays away for, one per id: a person's /optout,
// bot-wide because the deployments already have separate databases, or a guild's /quiet.
//
// Each is on all the time or on a recurring schedule, and either can carry an end. Has answers all
// of them the same way, from memory, in constant time.
package silence

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/disgoorg/snowflake/v2"

	"github.com/be-sandaa/coucou/internal/schedule"
	"github.com/be-sandaa/coucou/internal/store"
)

// Entry is one live silence: Until is its end, zero for none, and Rule is nil for all the time.
type Entry struct {
	Until time.Time
	schedule.Window
}

// On reports whether e is on at now, from the occurrence cached at the last refresh.
func (e Entry) On(now time.Time) bool {
	if !e.Until.IsZero() && !now.Before(e.Until) {
		return false
	}
	return e.Rule == nil || e.Active(now)
}

// At is On for any instant, walking the rule rather than reading the cache. Off the hot path only.
func (e Entry) At(t time.Time) bool {
	if e.Rule != nil {
		e.Window.At(t)
	}
	return e.On(t)
}

// Store mirrors one silence table in memory: loaded once at boot, updated on command, never read
// back. voice.Humans runs per candidate channel per loop tick with no REST and no database, and a
// lookup per human would give that up — which is load-bearing for the memory and latency targets.
type Store struct {
	list func(context.Context) ([]store.Silence, error)
	put  func(context.Context, store.Silence) error
	drop func(context.Context, snowflake.ID) error

	mu sync.RWMutex
	m  map[snowflake.ID]Entry
}

func newStore(
	list func(context.Context) ([]store.Silence, error),
	put func(context.Context, store.Silence) error,
	drop func(context.Context, snowflake.ID) error,
) *Store {
	return &Store{list: list, put: put, drop: drop, m: map[snowflake.ID]Entry{}}
}

// OptOuts is the people who asked the bot to stop counting them when it picks a channel.
func OptOuts(db store.Store) *Store { return newStore(db.ListOptOuts, db.SetOptOut, db.ClearOptOut) }

// Quiet is the guilds the bot leaves alone.
func Quiet(db store.Store) *Store { return newStore(db.ListQuiet, db.SetQuiet, db.ClearQuiet) }

func parse(o store.Silence, now time.Time) (Entry, error) {
	var e Entry
	if o.Until != nil {
		e.Until = *o.Until
	}
	if o.Rule == "" {
		return e, nil
	}
	w, err := schedule.Parse(o.Rule, o.Window, now)
	if err != nil {
		return e, err
	}
	e.Window = w
	return e, nil
}

// Load fills the set from the database. Call once, before the loop starts. The rows are each id's
// live one, oldest first, so should two be live the newer is what stays.
func (s *Store) Load(ctx context.Context) error {
	rows, err := s.list(ctx)
	if err != nil {
		return err
	}
	now := time.Now()
	m := make(map[snowflake.ID]Entry, len(rows))
	for _, r := range rows {
		e, err := parse(r, now)
		if err != nil {
			// A rule reaches the table only through a command, which parses it before writing, so
			// this is corruption rather than bad input. Held open rather than dropped: the side to
			// err on is the one somebody asked for, and off is the way back out.
			slog.Error("silence: unreadable rule, holding it open", slog.Any("id", r.ID), slog.Any("err", err))
			e = Entry{Until: e.Until}
		}
		m[r.ID] = e
	}
	s.mu.Lock()
	s.m = m
	s.mu.Unlock()
	return nil
}

// Has reports whether id is silenced right now. This is the hot path: once per human in every
// candidate channel, on every tick.
//
// An entry whose end has passed answers false and stays in the map. Deleting it here would mean
// taking the write lock on a read path to reclaim a few bytes, and the next Load drops it anyway.
func (s *Store) Has(id snowflake.ID) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.m[id]
	return ok && e.On(time.Now())
}

// Get is id's silence, unless it has none or it has ended.
func (s *Store) Get(id snowflake.ID) (Entry, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.m[id]
	return e, ok && (e.Until.IsZero() || time.Now().Before(e.Until))
}

// Set replaces whatever silence id had. The row is written before memory changes: a set that
// agreed with a failed write would answer correctly until the next restart and then forget.
func (s *Store) Set(ctx context.Context, o store.Silence) error {
	e, err := parse(o, time.Now())
	if err != nil {
		return err
	}
	if err := s.put(ctx, o); err != nil {
		return err
	}
	s.mu.Lock()
	s.m[o.ID] = e
	s.mu.Unlock()
	return nil
}

// Clear ends id's silence, whichever shape it had.
func (s *Store) Clear(ctx context.Context, id snowflake.ID) error {
	if err := s.drop(ctx, id); err != nil {
		return err
	}
	s.mu.Lock()
	delete(s.m, id)
	s.mu.Unlock()
	return nil
}

// Run keeps the cached windows current. Without it a schedule would stay frozen at whichever
// occurrence was live when it was set, which is the one thing a recurring silence cannot do.
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
	for id, e := range s.m {
		if e.Rule == nil {
			continue
		}
		e.Window.At(now)
		s.m[id] = e
	}
}
