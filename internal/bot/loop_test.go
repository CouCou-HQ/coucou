package bot

import (
	"context"
	"math"
	"strconv"
	"testing"
	"time"

	"github.com/disgoorg/snowflake/v2"

	"github.com/be-sandaa/coucou/internal/chaos"
	"github.com/be-sandaa/coucou/internal/settings"
	"github.com/be-sandaa/coucou/internal/silence"
	"github.com/be-sandaa/coucou/internal/store"
)

// fakeStore is a store.Store that only answers ListSettings. candidates never reaches the database,
// so everything else exists purely to satisfy the interface.
type fakeStore struct{ rows []store.Settings }

func (f *fakeStore) ListSettings(context.Context) ([]store.Settings, error) { return f.rows, nil }

func (f *fakeStore) Migrate(context.Context) error                               { return nil }
func (f *fakeStore) Ping(context.Context) error                                  { return nil }
func (f *fakeStore) Close()                                                      {}
func (f *fakeStore) UpsertSettings(context.Context, store.Settings) error        { return nil }
func (f *fakeStore) WritePlays(context.Context, []store.Play) error              { return nil }
func (f *fakeStore) WriteMisc(context.Context, []store.Misc) error               { return nil }
func (f *fakeStore) UpsertGuilds(context.Context, []store.Guild) error           { return nil }
func (f *fakeStore) MarkGuildLeft(context.Context, snowflake.ID) error           { return nil }
func (f *fakeStore) SeedSettings(context.Context, store.Defaults) (int64, error) { return 0, nil }

func (f *fakeStore) GlobalStats(context.Context) (store.GlobalStats, error) {
	return store.GlobalStats{}, nil
}

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

const guildBase = snowflake.ID(800000000000000000)

// loopWith builds a Loop whose settings map holds n guilds, each configured by cfg. No client:
// candidates never reaches Discord: it takes its lookups as arguments.
func loopWith(t *testing.T, n int, cfg func(i int, s *store.Settings)) *Loop {
	t.Helper()
	rows := make([]store.Settings, n)
	for i := range rows {
		rows[i] = store.Settings{Guild: guildBase + snowflake.ID(i), TZ: new("Europe/Brussels")}
		cfg(i, &rows[i])
	}
	st := settings.New(&fakeStore{rows: rows})
	if err := st.Load(context.Background()); err != nil {
		t.Fatalf("load settings: %v", err)
	}
	return &Loop{settings: st, chaos: chaos.New(&fakeStore{}), quiet: silence.Quiet(&fakeStore{})}
}

// guildIndex recovers the position a test guild was created at.
func guildIndex(g snowflake.ID) int {
	d := uint64(g - guildBase)
	if d > math.MaxInt32 {
		return -1
	}
	return int(d)
}

// oneHuman stands in for the cache lookup: every guild has a joinable channel with somebody in it.
func oneHuman(g snowflake.ID) (snowflake.ID, []snowflake.ID) {
	return g + 1, []snowflake.ID{g + 2}
}

func neverBusy(snowflake.ID) bool { return false }

func TestCandidatesRollsRoughlyTheConfiguredChance(t *testing.T) {
	const n = 1000
	l := loopWith(t, n, func(_ int, s *store.Settings) { s.Chance = 50 })

	got := len(l.candidates(time.Now(), oneHuman, neverBusy))
	// 50% of 1000 with +/-10% slack. A fair coin landing outside [400,600] over 1000 flips is
	// vanishingly unlikely, so this is not a flaky assertion.
	if got < 400 || got > 600 {
		t.Errorf("got %d candidates out of %d at 50%% chance, want roughly 500 (400-600)", got, n)
	}
}

func TestCandidatesSkipsZeroChance(t *testing.T) {
	l := loopWith(t, 500, func(_ int, s *store.Settings) { s.Chance = 0 })
	if got := l.candidates(time.Now(), oneHuman, neverBusy); len(got) != 0 {
		t.Errorf("got %d candidates, want 0 — chance 0 means never", len(got))
	}
}

func TestCandidatesNeverPicksAQuietGuild(t *testing.T) {
	const n = 1000
	// Every guild is at 100% chance, so only quiet can hold one back. Half are quiet always.
	isQuiet := func(i int) bool { return i%2 == 0 }
	l := loopWith(t, n, func(_ int, s *store.Settings) { s.Chance = 100 })
	for i := range n {
		if isQuiet(i) {
			if err := l.quiet.Set(t.Context(), store.Silence{ID: guildBase + snowflake.ID(i)}); err != nil {
				t.Fatalf("set quiet: %v", err)
			}
		}
	}

	got := l.candidates(time.Now(), oneHuman, neverBusy)
	if len(got) != n/2 {
		t.Fatalf("got %d candidates, want %d — every quiet guild must be skipped", len(got), n/2)
	}
	for _, p := range got {
		if isQuiet(guildIndex(p.guild)) {
			t.Fatalf("guild %d is in quiet hours and must not have been picked", p.guild)
		}
	}
}

func TestCandidatesNeverPicksABusyGuild(t *testing.T) {
	const n = 400
	l := loopWith(t, n, func(_ int, s *store.Settings) { s.Chance = 100 })

	busy := func(g snowflake.ID) bool { return (g-guildBase)%4 == 0 }
	got := l.candidates(time.Now(), oneHuman, busy)

	if want := n - n/4; len(got) != want {
		t.Fatalf("got %d candidates, want %d", len(got), want)
	}
	for _, p := range got {
		if busy(p.guild) {
			t.Fatalf("guild %d is already playing and must not have been picked", p.guild)
		}
	}
}

func TestCandidatesSkipsEmptyChannels(t *testing.T) {
	l := loopWith(t, 200, func(_ int, s *store.Settings) { s.Chance = 100 })
	empty := func(snowflake.ID) (snowflake.ID, []snowflake.ID) { return 0, nil }
	if got := l.candidates(time.Now(), empty, neverBusy); len(got) != 0 {
		t.Errorf("got %d candidates, want 0 — a channel with no humans is not a candidate", len(got))
	}
}

func TestCandidatesSuspenseStaysInRange(t *testing.T) {
	const suspense = 20
	l := loopWith(t, 500, func(_ int, s *store.Settings) {
		s.Chance = 100
		s.Suspense = suspense
	})

	got := l.candidates(time.Now(), oneHuman, neverBusy)
	if len(got) == 0 {
		t.Fatal("expected candidates at 100% chance")
	}
	for _, p := range got {
		if p.suspense < 3*time.Second || p.suspense > suspense*time.Second {
			t.Fatalf("suspense %s outside 3s..%ds", p.suspense, suspense)
		}
	}
}

// /suspense advertises a maximum, so every draw has to land inside it. A fixed floor of three
// seconds meant `/suspense 1` waited three — more than the guild asked for, on the one setting
// whose whole point is not overstaying.
func TestCandidatesSuspenseNeverExceedsTheConfiguredMax(t *testing.T) {
	for _, maximum := range []int{1, 2, 3, 4, 5, 20} {
		t.Run(strconv.Itoa(maximum), func(t *testing.T) {
			l := loopWith(t, 300, func(_ int, s *store.Settings) {
				s.Chance = 100
				s.Suspense = maximum
			})
			got := l.candidates(time.Now(), oneHuman, neverBusy)
			if len(got) == 0 {
				t.Fatal("expected candidates at 100% chance")
			}
			want := time.Duration(maximum) * time.Second
			for _, p := range got {
				if p.suspense <= 0 || p.suspense > want {
					t.Fatalf("suspense %s outside 1s..%s for /suspense %d", p.suspense, want, maximum)
				}
			}
		})
	}
}

func TestCandidatesWithSuspenseOffIsZero(t *testing.T) {
	l := loopWith(t, 100, func(_ int, s *store.Settings) { s.Chance = 100 })
	for _, p := range l.candidates(time.Now(), oneHuman, neverBusy) {
		if p.suspense != 0 {
			t.Fatalf("suspense = %s, want 0 when it is switched off", p.suspense)
		}
	}
}

// A fake-out rides on suspense: with suspense off there is nothing to sit through, so the stored
// percentage must never fire.
func TestCandidatesFakeOut(t *testing.T) {
	const n = 1000
	tests := []struct {
		name              string
		suspense, fakeOut int
		lo, hi            int
	}{
		{"off", 10, 0, 0, 0},
		{"inert without suspense", 0, 50, 0, 0},
		{"rolls roughly the configured share", 10, 50, 400, 600},
		{"a low share stays low", 10, 5, 10, 100},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := loopWith(t, n, func(_ int, s *store.Settings) {
				s.Chance = 100
				s.Suspense = tt.suspense
				s.FakeOut = tt.fakeOut
			})
			got := l.candidates(time.Now(), oneHuman, neverBusy)
			if len(got) != n {
				t.Fatalf("got %d candidates, want %d — a fake-out still visits", len(got), n)
			}
			fakes := 0
			for _, p := range got {
				if p.fakeOut {
					fakes++
				}
			}
			if fakes < tt.lo || fakes > tt.hi {
				t.Errorf("%d of %d picks faked out, want %d-%d", fakes, n, tt.lo, tt.hi)
			}
		})
	}
}

// An encore rides on the visit, not on suspense, so it rolls whatever suspense is set to.
func TestCandidatesEncore(t *testing.T) {
	const n = 1000
	tests := []struct {
		name             string
		suspense, encore int
		lo, hi           int
	}{
		{"zero never comes back", 10, 0, 0, 0},
		{"rolls with suspense off", 0, 50, 400, 600},
		{"rolls the same with suspense on", 10, 50, 400, 600},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := loopWith(t, n, func(_ int, s *store.Settings) {
				s.Chance, s.Suspense, s.Encore = 100, tt.suspense, tt.encore
			})
			encores := 0
			for _, p := range l.candidates(time.Now(), oneHuman, neverBusy) {
				if p.encore {
					encores++
				}
			}
			if encores < tt.lo || encores > tt.hi {
				t.Errorf("%d of %d picks will encore, want %d-%d", encores, n, tt.lo, tt.hi)
			}
		})
	}
}

func (f *fakeStore) ListOptOuts(context.Context) ([]store.Silence, error) { return nil, nil }
func (f *fakeStore) SetOptOut(context.Context, store.Silence) error       { return nil }
func (f *fakeStore) ClearOptOut(context.Context, snowflake.ID) error      { return nil }
func (f *fakeStore) ListQuiet(context.Context) ([]store.Silence, error)   { return nil, nil }
func (f *fakeStore) SetQuiet(context.Context, store.Silence) error        { return nil }
func (f *fakeStore) ClearQuiet(context.Context, snowflake.ID) error       { return nil }

func (f *fakeStore) ListChaos(context.Context) ([]store.Chaos, error) { return nil, nil }
func (f *fakeStore) AppendChaos(context.Context, store.Chaos) error   { return nil }

func (*fakeStore) PlaysHourly(context.Context, *snowflake.ID, time.Time) ([]store.PlayHour, error) {
	return nil, nil
}
func (*fakeStore) UserHourly(context.Context, *snowflake.ID, snowflake.ID, time.Time) ([]store.UserHour, error) {
	return nil, nil
}
func (*fakeStore) RefreshAnalytics(context.Context) error { return nil }
