package settings

import (
	"context"
	"sync"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"

	"github.com/be-sandaa/coucou/internal/store"
)

// TZ is nil only between a guild's settings row being seeded and its zone being filled in; see
// FillZones.
type Settings struct {
	Chance    int
	QuietFrom *int
	QuietTo   *int
	TZ        *string
	Suspense  int
	FakeOut   int
	Encore    int
}

type Store struct {
	db store.Store
	mu sync.RWMutex
	m  map[snowflake.ID]Settings
	tz sync.Map
}

func New(db store.Store) *Store { return &Store{db: db, m: map[snowflake.ID]Settings{}} }

func (s *Store) Load(ctx context.Context) error {
	rows, err := s.db.ListSettings(ctx)
	if err != nil {
		return err
	}
	m := make(map[snowflake.ID]Settings, len(rows))
	for _, r := range rows {
		m[r.Guild] = Settings{Chance: r.Chance, QuietFrom: r.QuietFrom, QuietTo: r.QuietTo, TZ: r.TZ, Suspense: r.Suspense, FakeOut: r.FakeOut, Encore: r.Encore}
	}
	s.mu.Lock()
	s.m = m
	s.mu.Unlock()
	return nil
}

func (s *Store) Get(guild snowflake.ID) Settings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if st, ok := s.m[guild]; ok {
		return st
	}
	return Settings{}
}

func (s *Store) Configured() map[snowflake.ID]Settings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[snowflake.ID]Settings, len(s.m))
	for k, v := range s.m {
		out[k] = v
	}
	return out
}

// Update writes the guild's settings and returns what they became. by is who asked; zero is the bot
// itself, seeding a guild it just joined. It is stored for the audit trigger, not read back.
func (s *Store) Update(ctx context.Context, guild, by snowflake.ID, fn func(*Settings)) (Settings, error) {
	next := s.Get(guild)
	fn(&next)
	if err := s.db.UpsertSettings(ctx, store.Settings{Guild: guild, Chance: next.Chance, QuietFrom: next.QuietFrom, QuietTo: next.QuietTo, TZ: next.TZ, Suspense: next.Suspense, FakeOut: next.FakeOut, Encore: next.Encore, UpdatedBy: by}); err != nil {
		return next, err
	}
	s.mu.Lock()
	s.m[guild] = next
	s.mu.Unlock()
	return next, nil
}

func (s *Store) loc(tz string) *time.Location {
	if v, ok := s.tz.Load(tz); ok {
		if l, ok := v.(*time.Location); ok {
			return l
		}
	}
	l, err := time.LoadLocation(tz)
	if err != nil {
		l = time.UTC
	}
	s.tz.Store(tz, l)
	return l
}

// Zone is the zone the guild's settings run in: the stored one. Nothing is worked out here at read
// time; a guild without one is a row FillZones has not reached yet, and reads as UTC until it has.
func Zone(st Settings) string {
	if st.TZ != nil {
		return *st.TZ
	}
	return time.UTC.String()
}

// GuessZone is the zone a guild's locale points at, or UTC for a locale spread across several.
func GuessZone(l discord.Locale) string {
	if tz, ok := localeZones[l]; ok {
		return tz
	}
	return time.UTC.String()
}

// FillZones stores a zone for every guild in locales that has none yet, guessed from its locale.
// Written rather than worked out on every read, so a guild's zone is a setting like the others: it
// shows in the table and the audit log, and it only changes when somebody changes it. A guild that
// already has one, chosen or filled earlier, is left alone. Returns how many it filled.
func (s *Store) FillZones(ctx context.Context, locales map[snowflake.ID]discord.Locale) (int, error) {
	n := 0
	for guild, l := range locales {
		if s.Get(guild).TZ != nil {
			continue
		}
		tz := GuessZone(l)
		// Actor zero: nobody asked for this, the bot is filling in what it could tell on its own.
		if _, err := s.Update(ctx, guild, 0, func(st *Settings) { st.TZ = &tz }); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

func (s *Store) IsQuiet(st Settings, now time.Time) bool {
	if st.QuietFrom == nil || st.QuietTo == nil || *st.QuietFrom == *st.QuietTo {
		return false
	}
	return QuietAt(*st.QuietFrom, *st.QuietTo, now.In(s.loc(Zone(st))).Hour())
}

// QuietAt is the window rule on its own, for one hour of the clock. Exported because the status
// readout draws all twenty-four and cannot go through IsQuiet, which resolves a zone for a single
// instant. One rule, so the drawing and the decision can never disagree about midnight.
func QuietAt(from, to, h int) bool {
	if from == to {
		return false
	}
	if from < to {
		return h >= from && h < to
	}
	return h >= from || h < to
}

func ValidTZ(tz string) bool { _, err := time.LoadLocation(tz); return err == nil }
