// Package optout holds the set of users who asked the bot to stop counting them when it picks a
// channel to drop in on. Bot-wide rather than per guild: the deployments already have separate
// databases, so that is as narrow as the choice needs to be.
//
// An opt-out comes in three shapes — indefinite, until a deadline, or on a recurring schedule —
// and Has answers all three the same way, from memory, in constant time.
package optout

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/disgoorg/snowflake/v2"

	"github.com/be-sandaa/coucou/internal/schedule"
	"github.com/be-sandaa/coucou/internal/store"
)

// entry is one person's opt-out: a deadline, or a schedule when Rule is set.
type entry struct {
	until time.Time // deadline; zero means none
	schedule.Window
}

func (e entry) active(now time.Time) bool {
	if e.Rule != nil {
		return e.Active(now)
	}
	return e.until.IsZero() || now.Before(e.until)
}

// Store mirrors the opt-out table in memory: loaded once at boot, updated on command, never read
// back. voice.Humans runs per candidate channel per loop tick with no REST and no database, and a
// lookup per human would give that up — which is load-bearing for the memory and latency targets.
type Store struct {
	db store.Store
	mu sync.RWMutex
	m  map[snowflake.ID]entry
}

func New(db store.Store) *Store { return &Store{db: db, m: map[snowflake.ID]entry{}} }

// parse turns a stored row into the entry Has reads.
func parse(o store.OptOut, now time.Time) (entry, error) {
	var e entry
	if o.Until != nil {
		e.until = *o.Until
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

// Load fills the set from the database. Call once, before the loop starts. Rows whose deadline has
// already passed are filtered by the query, so nothing expired is ever loaded.
func (s *Store) Load(ctx context.Context) error {
	rows, err := s.db.ListOptOuts(ctx)
	if err != nil {
		return err
	}
	now := time.Now()
	m := make(map[snowflake.ID]entry, len(rows))
	for _, r := range rows {
		e, err := parse(r, now)
		if err != nil {
			// A rule reaches the table only through the command, which parses it before writing,
			// so this is corruption rather than bad input. Fall back to the indefinite opt-out:
			// honouring the request the person made matters more than honouring its schedule, and
			// /optout off is the way back out.
			slog.Error("optout: unreadable rule, holding the opt-out open",
				slog.Any("user", r.User), slog.Any("err", err))
			e = entry{}
		}
		m[r.User] = e
	}
	s.mu.Lock()
	s.m = m
	s.mu.Unlock()
	return nil
}

// Has reports whether user is opted out right now. This is the hot path: once per human in every
// candidate channel, on every tick.
//
// An entry whose deadline has passed answers false and stays in the map. Deleting it here would
// mean taking the write lock on a read path to reclaim a few bytes, and the next Load drops it
// anyway — the map is bounded by how many people have ever run the command, not by time.
func (s *Store) Has(user snowflake.ID) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.m[user]
	return ok && e.active(time.Now())
}

// Set replaces whatever opt-out the user had. The row is written before memory changes: a set that
// agreed with a failed write would answer correctly until the next restart and then forget.
func (s *Store) Set(ctx context.Context, o store.OptOut) error {
	e, err := parse(o, time.Now())
	if err != nil {
		return err
	}
	if err := s.db.SetOptOut(ctx, o); err != nil {
		return err
	}
	s.mu.Lock()
	s.m[o.User] = e
	s.mu.Unlock()
	return nil
}

// Clear puts a user back, whichever of the three shapes they had.
func (s *Store) Clear(ctx context.Context, user snowflake.ID) error {
	if err := s.db.ClearOptOut(ctx, user); err != nil {
		return err
	}
	s.mu.Lock()
	delete(s.m, user)
	s.mu.Unlock()
	return nil
}

// Run keeps the cached windows current. Without it a schedule would stay frozen at whichever
// occurrence was live when it was set, which is the one thing a recurring opt-out cannot do.
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
		e.At(now)
		s.m[id] = e
	}
}
