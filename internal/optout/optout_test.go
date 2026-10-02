package optout

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/disgoorg/snowflake/v2"
	"github.com/teambition/rrule-go"

	"github.com/be-sandaa/coucou/internal/schedule"
	"github.com/be-sandaa/coucou/internal/store"
)

const (
	userA = snowflake.ID(100)
	userB = snowflake.ID(200)
)

var errWrite = errors.New("database is asleep")

// fakeStore is the three opt-out methods and nothing else that matters; the rest of store.Store is
// here only to satisfy the interface.
type fakeStore struct {
	rows     []store.OptOut
	failList error
	failSet  error
	failClr  error
}

func (f *fakeStore) ListOptOuts(context.Context) ([]store.OptOut, error) {
	return f.rows, f.failList
}

func (f *fakeStore) SetOptOut(_ context.Context, o store.OptOut) error {
	if f.failSet != nil {
		return f.failSet
	}
	f.rows = append(f.rows, o)
	return nil
}

// until builds the row /optout for writes, d from now.
func until(u snowflake.ID, d time.Duration) store.OptOut {
	t := time.Now().Add(d)
	return store.OptOut{User: u, Until: &t}
}

// indefinite and timed build the rows a backend would hand back, so a test says which kind it means.
func indefinite(u snowflake.ID) store.OptOut { return store.OptOut{User: u} }
func timed(u snowflake.ID, d time.Duration) store.OptOut {
	t := time.Now().Add(d)
	return store.OptOut{User: u, Until: &t}
}

func (f *fakeStore) ClearOptOut(context.Context, snowflake.ID) error { return f.failClr }

func (f *fakeStore) Migrate(context.Context) error                               { return nil }
func (f *fakeStore) Ping(context.Context) error                                  { return nil }
func (f *fakeStore) Close()                                                      {}
func (f *fakeStore) ListSettings(context.Context) ([]store.Settings, error)      { return nil, nil }
func (f *fakeStore) UpsertSettings(context.Context, store.Settings) error        { return nil }
func (f *fakeStore) WritePlays(context.Context, []store.Play) error              { return nil }
func (f *fakeStore) WriteMisc(context.Context, []store.Misc) error               { return nil }
func (f *fakeStore) UpsertGuilds(context.Context, []store.Guild) error           { return nil }
func (f *fakeStore) MarkGuildLeft(context.Context, snowflake.ID) error           { return nil }
func (f *fakeStore) SeedSettings(context.Context, store.Defaults) (int64, error) { return 0, nil }

func (f *fakeStore) MarkGuildsLeftExcept(context.Context, []snowflake.ID) ([]snowflake.ID, error) {
	return nil, nil
}

func (f *fakeStore) SeedSettingsFor(context.Context, snowflake.ID, store.Defaults) error { return nil }

func (f *fakeStore) GuildStats(context.Context, snowflake.ID) (store.GuildStats, error) {
	return store.GuildStats{}, nil
}

func (f *fakeStore) UserStats(context.Context, *snowflake.ID, snowflake.ID) (store.UserStats, error) {
	return store.UserStats{}, nil
}

func (*fakeStore) HeardSounds(context.Context, *snowflake.ID, snowflake.ID, []string) ([]string, error) {
	return nil, nil
}

func (f *fakeStore) GlobalStats(context.Context) (store.GlobalStats, error) {
	return store.GlobalStats{}, nil
}

func (f *fakeStore) Leaderboard(context.Context, snowflake.ID, string, int) ([]store.Row, error) {
	return nil, nil
}

func (f *fakeStore) TopGuilds(context.Context, int) ([]store.Row, error) { return nil, nil }
func (*fakeStore) UserRank(context.Context, snowflake.ID, snowflake.ID, time.Time) (store.UserRank, error) {
	return store.UserRank{}, nil
}
func (*fakeStore) UserRecent(context.Context, snowflake.ID, time.Time) (store.UserCounts, error) {
	return store.UserCounts{}, nil
}
func (*fakeStore) GuildRecent(context.Context, snowflake.ID, time.Time) (store.GuildRecent, error) {
	return store.GuildRecent{}, nil
}
func (*fakeStore) Cuts(context.Context, string, time.Time, time.Time) ([]float64, error) {
	return nil, nil
}

// Load is the only read: the loop asks Has once per human per tick, and that has to be answered
// from memory or the memory target goes with it.
func TestLoadThenAnswerFromMemory(t *testing.T) {
	db := &fakeStore{rows: []store.OptOut{indefinite(userA)}}
	s := New(db)

	if s.Has(userA) {
		t.Error("Has answered true before Load")
	}
	if err := s.Load(t.Context()); err != nil {
		t.Fatalf("load: %v", err)
	}
	if !s.Has(userA) {
		t.Errorf("%s is not opted out after loading a row for them", userA)
	}
	if s.Has(userB) {
		t.Errorf("%s is opted out with no row", userB)
	}
}

// Load replaces rather than merges, so a set that shrank in the database shrinks here too.
func TestLoadReplaces(t *testing.T) {
	db := &fakeStore{rows: []store.OptOut{indefinite(userA)}}
	s := New(db)
	if err := s.Load(t.Context()); err != nil {
		t.Fatalf("load: %v", err)
	}

	db.rows = []store.OptOut{indefinite(userB)}
	if err := s.Load(t.Context()); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if s.Has(userA) {
		t.Errorf("%s survived a reload that no longer lists them", userA)
	}
	if !s.Has(userB) {
		t.Errorf("%s is missing after a reload that lists them", userB)
	}
}

// The whole point of a timed opt-out is that it stops on its own, with nothing scheduled to end it.
func TestATimedOptOutExpiresOnItsOwn(t *testing.T) {
	s := New(&fakeStore{})

	if err := s.Set(t.Context(), until(userA, -time.Second)); err != nil {
		t.Fatalf("set: %v", err)
	}
	if s.Has(userA) {
		t.Errorf("%s is opted out although the deadline has passed", userA)
	}

	if err := s.Set(t.Context(), until(userB, time.Hour)); err != nil {
		t.Fatalf("set: %v", err)
	}
	if !s.Has(userB) {
		t.Errorf("%s is not opted out although the deadline is an hour away", userB)
	}
}

// The zero time is the indefinite opt-out, and it must not read as "expired at the epoch".
func TestTheZeroTimeMeansIndefinite(t *testing.T) {
	s := New(&fakeStore{})
	if err := s.Set(t.Context(), store.OptOut{User: userA}); err != nil {
		t.Fatalf("set: %v", err)
	}
	if !s.Has(userA) {
		t.Errorf("%s with a zero deadline is not opted out", userA)
	}
}

// Opting out again is how a deadline is extended or dropped, so the second Set has to win.
func TestSetOverwritesTheDeadline(t *testing.T) {
	s := New(&fakeStore{})
	if err := s.Set(t.Context(), until(userA, -time.Second)); err != nil {
		t.Fatalf("set expired: %v", err)
	}
	if err := s.Set(t.Context(), store.OptOut{User: userA}); err != nil {
		t.Fatalf("set indefinite: %v", err)
	}
	if !s.Has(userA) {
		t.Errorf("%s is still expired after being opted out indefinitely", userA)
	}
}

// A row loaded with a deadline behaves like one set with it -- Load is the only other way in.
func TestLoadCarriesTheDeadline(t *testing.T) {
	s := New(&fakeStore{rows: []store.OptOut{timed(userA, time.Hour), timed(userB, -time.Hour)}})
	if err := s.Load(t.Context()); err != nil {
		t.Fatalf("load: %v", err)
	}
	if !s.Has(userA) {
		t.Errorf("%s loaded with an hour left is not opted out", userA)
	}
	if s.Has(userB) {
		t.Errorf("%s loaded already expired is opted out", userB)
	}
}

func TestSetAndClear(t *testing.T) {
	s := New(&fakeStore{})

	if err := s.Set(t.Context(), store.OptOut{User: userA}); err != nil {
		t.Fatalf("set: %v", err)
	}
	if !s.Has(userA) {
		t.Errorf("%s is not opted out after Set", userA)
	}
	if err := s.Clear(t.Context(), userA); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if s.Has(userA) {
		t.Errorf("%s is still opted out after Clear", userA)
	}
}

// A write that failed must not be believed: memory agreeing with it would answer correctly until
// the next restart and then silently forget what the person asked for.
func TestAFailedWriteLeavesMemoryAlone(t *testing.T) {
	t.Run("set", func(t *testing.T) {
		s := New(&fakeStore{failSet: errWrite})
		if err := s.Set(t.Context(), store.OptOut{User: userA}); !errors.Is(err, errWrite) {
			t.Fatalf("Set returned %v, want %v", err, errWrite)
		}
		if s.Has(userA) {
			t.Errorf("%s is opted out in memory although the write failed", userA)
		}
	})

	t.Run("clear", func(t *testing.T) {
		db := &fakeStore{rows: []store.OptOut{indefinite(userA)}}
		s := New(db)
		if err := s.Load(t.Context()); err != nil {
			t.Fatalf("load: %v", err)
		}

		db.failClr = errWrite
		if err := s.Clear(t.Context(), userA); !errors.Is(err, errWrite) {
			t.Fatalf("Clear returned %v, want %v", err, errWrite)
		}
		if !s.Has(userA) {
			t.Errorf("%s was cleared in memory although the delete failed", userA)
		}
	})
}

func TestLoadReturnsTheStoreError(t *testing.T) {
	s := New(&fakeStore{failList: errWrite})
	if err := s.Load(t.Context()); !errors.Is(err, errWrite) {
		t.Errorf("Load returned %v, want %v", err, errWrite)
	}
}

// weekdays9to5 is the schedule the guided subcommand writes: weekdays at 09:00, in a fixed zone so
// nothing here depends on the machine's.
func weekdays9(t *testing.T) *rrule.RRule {
	t.Helper()
	loc, err := time.LoadLocation("Europe/Brussels")
	if err != nil {
		t.Fatalf("load zone: %v", err)
	}
	r, err := rrule.NewRRule(rrule.ROption{
		Freq:      rrule.WEEKLY,
		Byweekday: []rrule.Weekday{rrule.MO, rrule.TU, rrule.WE, rrule.TH, rrule.FR},
		Byhour:    []int{9}, Byminute: []int{0}, Bysecond: []int{0},
		Dtstart: time.Date(2026, 1, 1, 0, 0, 0, 0, loc),
	})
	if err != nil {
		t.Fatalf("build rule: %v", err)
	}
	return r
}

func at(t *testing.T, month time.Month, day, hour, minute int) time.Time {
	t.Helper()
	loc, err := time.LoadLocation("Europe/Brussels")
	if err != nil {
		t.Fatalf("load zone: %v", err)
	}
	return time.Date(2026, month, day, hour, minute, 0, 0, loc)
}

// The window is what Has actually answers from, so its edges are the contract. Tested on entry
// rather than through Has because Has reads the wall clock, and these cases are about instants.
func TestARecurringOptOutIsOnlyActiveInsideItsWindow(t *testing.T) {
	e := entry{Window: schedule.Window{Rule: weekdays9(t), Length: 8 * time.Hour}}

	cases := []struct {
		name string
		now  time.Time
		want bool
	}{
		{"tuesday mid-morning", at(t, time.September, 22, 10, 30), true},
		{"tuesday as it opens", at(t, time.September, 22, 9, 0), true},
		{"tuesday a minute early", at(t, time.September, 22, 8, 59), false},
		{"tuesday as it closes", at(t, time.September, 22, 17, 0), false},
		{"tuesday evening", at(t, time.September, 22, 20, 0), false},
		{"sunday mid-morning", at(t, time.September, 20, 10, 30), false},
		// November is CET where September is CEST: the rule is anchored to the zone, so 09:00
		// stays 09:00 rather than sliding an hour when the clocks go back.
		{"after the clocks go back", at(t, time.November, 17, 10, 30), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e.At(c.now) // what the refresher does a minute at a time
			if got := e.active(c.now); got != c.want {
				t.Errorf("active(%v) = %v, want %v (window %v..%v)", c.now, got, c.want, e.From, e.To)
			}
		})
	}
}

// Without the refresher a schedule stays frozen at whichever occurrence was live when it was set,
// which is the one thing a recurring opt-out cannot do.
func TestRefreshMovesTheWindowOn(t *testing.T) {
	s := New(&fakeStore{})
	s.m[userA] = entry{Window: schedule.Window{Rule: weekdays9(t), Length: 8 * time.Hour}}

	s.refresh(at(t, time.September, 21, 10, 0)) // Monday
	if got := s.m[userA].From; !got.Equal(at(t, time.September, 21, 9, 0)) {
		t.Fatalf("window starts %v, want Monday 09:00", got)
	}
	s.refresh(at(t, time.September, 22, 10, 0)) // Tuesday
	if got := s.m[userA].From; !got.Equal(at(t, time.September, 22, 9, 0)) {
		t.Errorf("window starts %v, want Tuesday 09:00 after a refresh a day later", got)
	}
}

// A rule only reaches the table through the command, which parses it first, so an unreadable one
// is corruption. Honouring the request matters more than honouring its schedule.
func TestAnUnreadableRuleHoldsTheOptOutOpen(t *testing.T) {
	s := New(&fakeStore{rows: []store.OptOut{{User: userA, Rule: "RRULE:FREQ=NONSENSE", Window: time.Hour}}})
	if err := s.Load(t.Context()); err != nil {
		t.Fatalf("load: %v", err)
	}
	if !s.Has(userA) {
		t.Errorf("%s was let back in by a rule the bot could not read", userA)
	}
}

func (f *fakeStore) ListChaos(context.Context) ([]store.Chaos, error) { return nil, nil }
func (f *fakeStore) AppendChaos(context.Context, store.Chaos) error   { return nil }

func (*fakeStore) PlaysHourly(context.Context, *snowflake.ID, time.Time) ([]store.PlayHour, error) {
	return nil, nil
}
func (*fakeStore) UserHourly(context.Context, *snowflake.ID, snowflake.ID, time.Time) ([]store.UserHour, error) {
	return nil, nil
}
func (*fakeStore) RefreshAnalytics(context.Context) error { return nil }
