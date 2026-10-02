package silence

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

// fakeStore is one silence table: the three functions a Store is built from.
type fakeStore struct {
	rows     []store.Silence
	failList error
	failSet  error
	failClr  error
}

func (f *fakeStore) list(context.Context) ([]store.Silence, error) { return f.rows, f.failList }

func (f *fakeStore) set(_ context.Context, o store.Silence) error {
	if f.failSet != nil {
		return f.failSet
	}
	f.rows = append(f.rows, o)
	return nil
}

func (f *fakeStore) clear(context.Context, snowflake.ID) error { return f.failClr }

func newFake(f *fakeStore) *Store { return newStore(f.list, f.set, f.clear) }

// until builds the row /optout for writes, d from now.
func until(u snowflake.ID, d time.Duration) store.Silence {
	t := time.Now().Add(d)
	return store.Silence{ID: u, Until: &t}
}

// indefinite and timed build the rows a backend would hand back, so a test says which kind it means.
func indefinite(u snowflake.ID) store.Silence { return store.Silence{ID: u} }
func timed(u snowflake.ID, d time.Duration) store.Silence {
	t := time.Now().Add(d)
	return store.Silence{ID: u, Until: &t}
}

// Load is the only read: the loop asks Has once per human per tick, and that has to be answered
// from memory or the memory target goes with it.
func TestLoadThenAnswerFromMemory(t *testing.T) {
	db := &fakeStore{rows: []store.Silence{indefinite(userA)}}
	s := newFake(db)

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
	db := &fakeStore{rows: []store.Silence{indefinite(userA)}}
	s := newFake(db)
	if err := s.Load(t.Context()); err != nil {
		t.Fatalf("load: %v", err)
	}

	db.rows = []store.Silence{indefinite(userB)}
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
	s := newFake(&fakeStore{})

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
	s := newFake(&fakeStore{})
	if err := s.Set(t.Context(), store.Silence{ID: userA}); err != nil {
		t.Fatalf("set: %v", err)
	}
	if !s.Has(userA) {
		t.Errorf("%s with a zero deadline is not opted out", userA)
	}
}

// Opting out again is how a deadline is extended or dropped, so the second Set has to win.
func TestSetOverwritesTheDeadline(t *testing.T) {
	s := newFake(&fakeStore{})
	if err := s.Set(t.Context(), until(userA, -time.Second)); err != nil {
		t.Fatalf("set expired: %v", err)
	}
	if err := s.Set(t.Context(), store.Silence{ID: userA}); err != nil {
		t.Fatalf("set indefinite: %v", err)
	}
	if !s.Has(userA) {
		t.Errorf("%s is still expired after being opted out indefinitely", userA)
	}
}

// A row loaded with a deadline behaves like one set with it -- Load is the only other way in.
func TestLoadCarriesTheDeadline(t *testing.T) {
	s := newFake(&fakeStore{rows: []store.Silence{timed(userA, time.Hour), timed(userB, -time.Hour)}})
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
	s := newFake(&fakeStore{})

	if err := s.Set(t.Context(), store.Silence{ID: userA}); err != nil {
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
		s := newFake(&fakeStore{failSet: errWrite})
		if err := s.Set(t.Context(), store.Silence{ID: userA}); !errors.Is(err, errWrite) {
			t.Fatalf("Set returned %v, want %v", err, errWrite)
		}
		if s.Has(userA) {
			t.Errorf("%s is opted out in memory although the write failed", userA)
		}
	})

	t.Run("clear", func(t *testing.T) {
		db := &fakeStore{rows: []store.Silence{indefinite(userA)}}
		s := newFake(db)
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
	s := newFake(&fakeStore{failList: errWrite})
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
	e := Entry{Window: schedule.Window{Rule: weekdays9(t), Length: 8 * time.Hour}}

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
			if got := e.At(c.now); got != c.want {
				t.Errorf("At(%v) = %v, want %v (window %v..%v)", c.now, got, c.want, e.From, e.To)
			}
		})
	}
}

// Without the refresher a schedule stays frozen at whichever occurrence was live when it was set,
// which is the one thing a recurring opt-out cannot do.
func TestRefreshMovesTheWindowOn(t *testing.T) {
	s := newFake(&fakeStore{})
	s.m[userA] = Entry{Window: schedule.Window{Rule: weekdays9(t), Length: 8 * time.Hour}}

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
	s := newFake(&fakeStore{rows: []store.Silence{{ID: userA, Rule: "RRULE:FREQ=NONSENSE", Window: time.Hour}}})
	if err := s.Load(t.Context()); err != nil {
		t.Fatalf("load: %v", err)
	}
	if !s.Has(userA) {
		t.Errorf("%s was let back in by a rule the bot could not read", userA)
	}
}

// An end applies to a schedule too: the window can be open and the silence still over.
func TestAScheduleWithAnEndStopsAtIt(t *testing.T) {
	e := Entry{Window: schedule.Window{Rule: weekdays9(t), Length: 8 * time.Hour}}
	e.Until = at(t, time.September, 22, 12, 0)
	if !e.At(at(t, time.September, 22, 11, 0)) {
		t.Error("off inside the window before its end")
	}
	if e.At(at(t, time.September, 22, 13, 0)) {
		t.Error("on inside the window after its end")
	}
}
