// Package store_test drives both backends through the same behavioural suite. sqlite always runs
// against a temp file; postgres runs only when TEST_DATABASE_URL points at a scratch database.
// Anything asserted here is part of the Store contract, not of one backend's SQL.
package store_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/disgoorg/snowflake/v2"

	"github.com/be-sandaa/coucou/internal/store"
	_ "github.com/be-sandaa/coucou/internal/store/pg"
	_ "github.com/be-sandaa/coucou/internal/store/sqlite"
)

type backend struct {
	name string
	open func(t *testing.T) store.Store
}

func backends(t *testing.T) []backend {
	t.Helper()
	out := []backend{{
		name: "sqlite",
		open: func(t *testing.T) store.Store {
			t.Helper()
			return openMigrated(t, "sqlite://"+filepath.Join(t.TempDir(), "coucou.db"))
		},
	}}
	if url := os.Getenv("TEST_DATABASE_URL"); url != "" {
		out = append(out, backend{
			name: "postgres",
			open: func(t *testing.T) store.Store {
				t.Helper()
				s := openMigrated(t, url)
				// Every subtest shares the one real database, so isolation comes from unique guild
				// ids per test rather than from clearing tables behind the interface's back.
				if _, err := s.MarkGuildsLeftExcept(context.Background(), nil); err != nil {
					t.Fatal(err)
				}
				return s
			},
		})
	}
	return out
}

func openMigrated(t *testing.T, url string) store.Store {
	t.Helper()
	ctx := context.Background()
	s, err := store.Open(ctx, url)
	if err != nil {
		t.Fatalf("open %s: %v", url, err)
	}
	t.Cleanup(s.Close)
	if err := store.WaitAndMigrate(ctx, s); err != nil {
		t.Fatalf("migrate %s: %v", url, err)
	}
	return s
}

const (
	tzBrussels = "Europe/Brussels"
	trigLoop   = "loop"
	trigCmd    = "command"
	trigEncore = "encore"
	boardHeard = "heard"
	reasonFail = "join_fail"
	reasonFake = "fakeout"
	reasonGone = "empty"
	boardFled  = "fled"
)

// Each test uses its own guild ids so a shared postgres database cannot leak rows between them --
// and the base is per-process, because a scratch postgres keeps its rows between runs and a fixed
// base would make the second run read the first run's plays.
var nextGuild = snowflake.ID(700000000000000000 + uint64(time.Now().UnixNano())%1e17)

func guildID(t *testing.T) snowflake.ID {
	t.Helper()
	nextGuild += 1000
	return nextGuild
}

// userID draws from the same counter: opt-outs are keyed by user rather than guild, and a shared
// postgres needs them unique for the same reason.
func userID(t *testing.T) snowflake.ID {
	t.Helper()
	return guildID(t)
}

func run(t *testing.T, name string, fn func(t *testing.T, s store.Store)) {
	for _, b := range backends(t) {
		t.Run(name+"/"+b.name, func(t *testing.T) { fn(t, b.open(t)) })
	}
}

func find(t *testing.T, s store.Store, g snowflake.ID) store.Settings {
	t.Helper()
	rows, err := s.ListSettings(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, r := range rows {
		if r.Guild == g {
			return r
		}
	}
	t.Fatalf("no settings row for guild %d", g)
	return store.Settings{}
}

func TestSettingsRoundTrip(t *testing.T) {
	run(t, "settings", func(t *testing.T, s store.Store) {
		g := guildID(t)
		from, to := 23, 8

		if err := s.UpsertSettings(context.Background(), store.Settings{
			Guild: g, Chance: 42, QuietFrom: &from, QuietTo: &to, TZ: new(tzBrussels), Suspense: 7, FakeOut: 30, Encore: 20,
		}); err != nil {
			t.Fatalf("upsert: %v", err)
		}

		got := find(t, s, g)
		if got.Chance != 42 || got.Suspense != 7 || got.FakeOut != 30 || got.Encore != 20 || !is(got.TZ, tzBrussels) {
			t.Errorf("got %+v", got)
		}
		if !is(got.QuietFrom, 23) {
			t.Errorf("QuietFrom did not round-trip: %v", got.QuietFrom)
		}
		if !is(got.QuietTo, 8) {
			t.Errorf("QuietTo did not round-trip: %v", got.QuietTo)
		}
	})
}

// Clearing quiet hours must store real NULLs, not zeroes — the loop reads nil as "no quiet hours".
// The zone likewise: nil is a guild that never chose one, and an empty string would read as chosen.
func TestUpsertSettingsClearsQuietHours(t *testing.T) {
	run(t, "clear-quiet", func(t *testing.T, s store.Store) {
		ctx := context.Background()
		g := guildID(t)
		from, to := 23, 8
		if err := s.UpsertSettings(ctx, store.Settings{
			Guild: g, Chance: 42, QuietFrom: &from, QuietTo: &to, TZ: new(tzBrussels), Suspense: 7,
		}); err != nil {
			t.Fatalf("upsert: %v", err)
		}

		if err := s.UpsertSettings(ctx, store.Settings{Guild: g, Chance: 5}); err != nil {
			t.Fatalf("upsert nil quiet: %v", err)
		}
		got := find(t, s, g)
		if got.QuietFrom != nil || got.QuietTo != nil {
			t.Errorf("quiet hours should be nil, got %v %v", got.QuietFrom, got.QuietTo)
		}
		if got.Chance != 5 || got.TZ != nil {
			t.Errorf("upsert did not overwrite: %+v", got)
		}
	})
}

func seedPlays(t *testing.T, s store.Store) (guild, loud, quiet snowflake.ID) {
	t.Helper()
	guild = guildID(t)
	ch := snowflake.ID(900000000000000001)
	loud = snowflake.ID(111111111111111111)
	quiet = snowflake.ID(222222222222222222)
	now := time.Now().UTC()

	plays := []store.Play{
		{At: now.Add(-3 * time.Minute), Guild: guild, Channel: ch, Sound: "a", Trigger: trigLoop,
			ListenerIDs: []snowflake.ID{loud, quiet}, OK: true, Duration: 1200 * time.Millisecond},
		{At: now.Add(-2 * time.Minute), Guild: guild, Channel: ch, Sound: "a", Trigger: trigLoop,
			ListenerIDs: []snowflake.ID{loud}, OK: true, Duration: 900 * time.Millisecond},
		{At: now.Add(-time.Minute), Guild: guild, Channel: ch, Sound: "b", Trigger: trigLoop,
			ListenerIDs: []snowflake.ID{loud}, OK: true, Duration: 900 * time.Millisecond},
		{At: now, Guild: guild, Channel: ch, Sound: "b", Trigger: trigLoop,
			ListenerIDs: []snowflake.ID{quiet}, OK: false, Reason: reasonFail, Duration: 10 * time.Millisecond},
	}
	if err := s.WritePlays(context.Background(), plays); err != nil {
		t.Fatalf("WritePlays: %v", err)
	}
	return guild, loud, quiet
}

func TestHeardLeaderboardRanksByAttendance(t *testing.T) {
	run(t, "heard", func(t *testing.T, s store.Store) {
		guild, loud, quiet := seedPlays(t, s)

		rows, err := s.Leaderboard(context.Background(), guild, boardHeard, 30)
		if err != nil {
			t.Fatalf("Leaderboard: %v", err)
		}
		if len(rows) != 2 {
			t.Fatalf("got %d rows, want 2: %+v", len(rows), rows)
		}
		if rows[0].Key != loud.String() || rows[0].N != 3 {
			t.Errorf("first row = %+v, want %s with 3", rows[0], loud)
		}
		if rows[1].Key != quiet.String() || rows[1].N != 1 {
			t.Errorf("second row = %+v, want %s with 1 (the failed play logs no listeners)", rows[1], quiet)
		}
	})
}

func TestGuildStatsAfterPlays(t *testing.T) {
	run(t, "stats", func(t *testing.T, s store.Store) {
		guild, _, _ := seedPlays(t, s)

		st, err := s.GuildStats(context.Background(), guild)
		if err != nil {
			t.Fatalf("GuildStats: %v", err)
		}
		if st.PlaysAll != 4 || st.Plays7d != 4 {
			t.Errorf("play counts = %d/%d, want 4/4", st.PlaysAll, st.Plays7d)
		}
		if st.TopSound == nil || (*st.TopSound != "a" && *st.TopSound != "b") {
			t.Errorf("TopSound = %v, want a or b", st.TopSound)
		}
		if st.FailRate7d == nil || *st.FailRate7d <= 0 {
			t.Errorf("FailRate7d = %v, want a positive rate", st.FailRate7d)
		}
	})
}

// A fake-out is a visit that was meant to play nothing, so it must not read as a broken play.
func TestGuildStatsFailRateIgnoresFakeOuts(t *testing.T) {
	tests := []struct {
		name    string
		reasons []string // "" is a successful play
		want    *float64
	}{
		{"fake-outs alone have no rate", []string{reasonFake, reasonFake}, nil},
		{"fake-outs are not failures", []string{"", reasonFake, reasonFake}, ptr(0.0)},
		{"real failures still count", []string{"", reasonFail, reasonFake, reasonFake}, ptr(0.5)},
	}
	for _, tt := range tests {
		run(t, tt.name, func(t *testing.T, s store.Store) {
			g := guildID(t)
			now := time.Now().UTC()
			plays := make([]store.Play, len(tt.reasons))
			for i, r := range tt.reasons {
				plays[i] = store.Play{At: now.Add(-time.Duration(i) * time.Minute), Guild: g, Channel: g + 1,
					Sound: "a", Trigger: trigLoop, OK: r == "", Reason: r, Duration: time.Second}
			}
			if err := s.WritePlays(context.Background(), plays); err != nil {
				t.Fatalf("WritePlays: %v", err)
			}
			st, err := s.GuildStats(context.Background(), g)
			if err != nil {
				t.Fatalf("GuildStats: %v", err)
			}
			switch {
			case tt.want == nil && st.FailRate7d != nil:
				t.Errorf("FailRate7d = %v, want nil", *st.FailRate7d)
			case tt.want != nil && (st.FailRate7d == nil || *st.FailRate7d != *tt.want):
				t.Errorf("FailRate7d = %v, want %v", st.FailRate7d, *tt.want)
			}
		})
	}
}

func ptr[T any](v T) *T { return &v }

func is[T comparable](p *T, v T) bool { return p != nil && *p == v }

// An encore is its own trigger; a store whose check constraint still lists only loop and command
// would fail the whole batch it arrives in.
func TestEncorePlayIsRecorded(t *testing.T) {
	run(t, "encore", func(t *testing.T, s store.Store) {
		g, listener := guildID(t), userID(t)
		if err := s.WritePlays(context.Background(), []store.Play{{At: time.Now().UTC(), Guild: g, Channel: g + 1,
			Sound: "a", Trigger: trigEncore, ListenerIDs: []snowflake.ID{listener}, OK: true, Duration: time.Second}}); err != nil {
			t.Fatalf("WritePlays: %v", err)
		}
		st, err := s.GuildStats(context.Background(), g)
		if err != nil {
			t.Fatalf("GuildStats: %v", err)
		}
		if st.PlaysAll != 1 {
			t.Errorf("PlaysAll = %d, want 1", st.PlaysAll)
		}
	})
}

func TestGuildStatsOnEmptyGuild(t *testing.T) {
	run(t, "empty-stats", func(t *testing.T, s store.Store) {
		st, err := s.GuildStats(context.Background(), guildID(t))
		if err != nil {
			t.Fatalf("GuildStats on an empty guild must not error: %v", err)
		}
		if st.PlaysAll != 0 || st.Plays7d != 0 {
			t.Errorf("counts = %d/%d, want 0/0", st.PlaysAll, st.Plays7d)
		}
		if st.FailRate7d != nil {
			t.Errorf("FailRate7d = %v, want nil", *st.FailRate7d)
		}
		if st.TopSound != nil {
			t.Errorf("TopSound = %v, want nil", *st.TopSound)
		}
		if st.LoudestHour != nil {
			t.Errorf("LoudestHour = %v, want nil", *st.LoudestHour)
		}
		if st.AvgListeners != nil {
			t.Errorf("AvgListeners = %v, want nil", *st.AvgListeners)
		}
	})
}

func TestUserStatsCountsAttendanceAndRequests(t *testing.T) {
	run(t, "user-stats", func(t *testing.T, s store.Store) {
		ctx := context.Background()
		guild := guildID(t)
		ch := snowflake.ID(900000000000000002)
		me, other := userID(t), userID(t)
		now := time.Now().UTC()
		last := now.Add(-time.Minute)

		plays := []store.Play{
			{At: now.Add(-3 * time.Minute), Guild: guild, Channel: ch, Sound: "a", Trigger: trigLoop,
				ListenerIDs: []snowflake.ID{me, other}, OK: true, Duration: time.Second},
			{At: last, Guild: guild, Channel: ch, Sound: "b", Trigger: trigCmd, User: &me,
				ListenerIDs: []snowflake.ID{me}, OK: true, Duration: time.Second},
			{At: now, Guild: guild, Channel: ch, Sound: "c", Trigger: trigLoop,
				ListenerIDs: []snowflake.ID{other}, OK: true, Duration: time.Second},
		}
		if err := s.WritePlays(ctx, plays); err != nil {
			t.Fatalf("WritePlays: %v", err)
		}

		st, err := s.UserStats(ctx, &guild, me)
		if err != nil {
			t.Fatalf("UserStats: %v", err)
		}
		if st.Heard != 2 {
			t.Errorf("Heard = %d, want 2 -- the third play had a different listener", st.Heard)
		}
		if st.Triggered != 1 {
			t.Errorf("Triggered = %d, want 1", st.Triggered)
		}
		if st.LastHeard == nil {
			t.Fatal("LastHeard = nil, want the most recent play this user was in")
		}
		// Postgres stores a timestamptz and sqlite RFC3339Nano text, so the contract is the
		// instant rather than the representation.
		if got := st.LastHeard.Unix(); got != last.Unix() {
			t.Errorf("LastHeard = %v, want %v", st.LastHeard.UTC(), last)
		}
	})
}

func TestUserStatsOnUnseenUser(t *testing.T) {
	run(t, "user-stats-empty", func(t *testing.T, s store.Store) {
		st, err := s.UserStats(context.Background(), ptr(guildID(t)), userID(t))
		if err != nil {
			t.Fatalf("UserStats for a user with no plays must not error: %v", err)
		}
		if st.Heard != 0 || st.Triggered != 0 || st.Fled != 0 {
			t.Errorf("counts = %d/%d/%d, want 0/0/0", st.Heard, st.Triggered, st.Fled)
		}
		if st.LastHeard != nil {
			t.Errorf("LastHeard = %v, want nil", *st.LastHeard)
		}
	})
}

func TestHeardSoundsIsDistinctAndFilteredByTrigger(t *testing.T) {
	run(t, "heard-sounds", func(t *testing.T, s store.Store) {
		ctx := context.Background()
		guild, elsewhere := guildID(t), guildID(t)
		ch := snowflake.ID(900000000000000003)
		me, other := userID(t), userID(t)
		now := time.Now().UTC()
		play := func(g snowflake.ID, sound, trigger string, ok bool, who ...snowflake.ID) store.Play {
			return store.Play{At: now, Guild: g, Channel: ch, Sound: sound, Trigger: trigger,
				ListenerIDs: who, OK: ok, Duration: time.Second}
		}
		if err := s.WritePlays(ctx, []store.Play{
			play(guild, "a", trigLoop, true, me, other),
			play(guild, "a", trigLoop, true, me),
			play(guild, "b", trigLoop, true, me),
			play(guild, "cmd", trigCmd, true, me),
			play(guild, "failed", trigLoop, false, me),
			play(guild, "theirs", trigLoop, true, other),
			play(elsewhere, "away", trigLoop, true, me),
		}); err != nil {
			t.Fatalf("WritePlays: %v", err)
		}

		tests := []struct {
			name     string
			triggers []string
			want     []string
		}{
			{"loop", []string{trigLoop}, []string{"a", "b"}},
			{"loop and command", []string{trigLoop, trigCmd}, []string{"a", "b", "cmd"}},
			{"none", nil, []string{}},
		}
		for _, tt := range tests {
			got, err := s.HeardSounds(ctx, &guild, me, tt.triggers)
			if err != nil {
				t.Fatalf("%s: HeardSounds: %v", tt.name, err)
			}
			slices.Sort(got)
			if !slices.Equal(got, tt.want) {
				t.Errorf("%s: HeardSounds = %v, want %v", tt.name, got, tt.want)
			}
		}
	})
}

// A failed play keeps its fled rows, since everyone fleeing is what empties the room, but they
// must not count as heard.
func TestFledRoundTrip(t *testing.T) {
	run(t, "fled", func(t *testing.T, s store.Store) {
		ctx := context.Background()
		guild := guildID(t)
		ch := snowflake.ID(900000000000000003)
		regular, runner := userID(t), userID(t)
		now := time.Now().UTC()
		heardAt := now.Add(-2 * time.Minute)

		plays := []store.Play{
			{At: heardAt, Guild: guild, Channel: ch, Sound: "a", Trigger: trigLoop,
				ListenerIDs: []snowflake.ID{regular, runner}, FledIDs: []snowflake.ID{runner}, OK: true, Duration: time.Second},
			{At: now.Add(-time.Minute), Guild: guild, Channel: ch, Sound: "a", Trigger: trigLoop,
				ListenerIDs: []snowflake.ID{regular, runner}, FledIDs: []snowflake.ID{regular, runner}, OK: false, Reason: reasonGone, Duration: time.Second},
			{At: now, Guild: guild, Channel: ch, Sound: "b", Trigger: trigLoop,
				ListenerIDs: []snowflake.ID{regular}, OK: true, Duration: time.Second},
		}
		if err := s.WritePlays(ctx, plays); err != nil {
			t.Fatalf("WritePlays: %v", err)
		}

		boards := []struct {
			board string
			want  []store.Row
		}{
			{boardFled, []store.Row{{Key: runner.String(), N: 2}, {Key: regular.String(), N: 1}}},
			{boardHeard, []store.Row{{Key: regular.String(), N: 2}, {Key: runner.String(), N: 1}}},
		}
		for _, b := range boards {
			rows, err := s.Leaderboard(ctx, guild, b.board, 30)
			if err != nil {
				t.Fatalf("Leaderboard(%s): %v", b.board, err)
			}
			if !slices.Equal(rows, b.want) {
				t.Errorf("Leaderboard(%s) = %+v, want %+v", b.board, rows, b.want)
			}
		}

		st, err := s.UserStats(ctx, &guild, runner)
		if err != nil {
			t.Fatalf("UserStats: %v", err)
		}
		if st.Heard != 1 || st.Fled != 2 {
			t.Errorf("Heard/Fled = %d/%d, want 1/2", st.Heard, st.Fled)
		}
		if st.LastHeard == nil || st.LastHeard.Unix() != heardAt.Unix() {
			t.Errorf("LastHeard = %v, want %v -- the failed play was not heard", st.LastHeard, heardAt)
		}
	})
}

func TestGlobalStatsOnEmptyDatabase(t *testing.T) {
	run(t, "empty-global", func(t *testing.T, s store.Store) {
		if _, err := s.GlobalStats(context.Background()); err != nil {
			t.Fatalf("GlobalStats on an empty database must not error: %v", err)
		}
	})
}

// seedGuilds registers three present guilds and returns their ids.
func seedGuilds(t *testing.T, s store.Store) (a, b, c snowflake.ID) {
	t.Helper()
	a, b, c = guildID(t), guildID(t), guildID(t)
	now := time.Now().UTC()
	if err := s.UpsertGuilds(context.Background(), []store.Guild{
		{ID: a, JoinedAt: now},
		{ID: b, JoinedAt: now},
		{ID: c, JoinedAt: now},
	}); err != nil {
		t.Fatalf("UpsertGuilds: %v", err)
	}
	return a, b, c
}

func TestAbsentGuildIsMarkedLeftExactlyOnce(t *testing.T) {
	run(t, "reconcile", func(t *testing.T, s store.Store) {
		ctx := context.Background()
		a, b, c := seedGuilds(t, s)

		left, err := s.MarkGuildsLeftExcept(ctx, []snowflake.ID{a, b})
		if err != nil {
			t.Fatalf("MarkGuildsLeftExcept: %v", err)
		}
		if len(left) != 1 || left[0] != c {
			t.Fatalf("left = %v, want [%d]", left, c)
		}

		again, err := s.MarkGuildsLeftExcept(ctx, []snowflake.ID{a, b})
		if err != nil {
			t.Fatalf("second MarkGuildsLeftExcept: %v", err)
		}
		if len(again) != 0 {
			t.Errorf("a guild already marked left must not be reported again, got %v", again)
		}
	})
}

func TestRejoiningClearsLeftAt(t *testing.T) {
	run(t, "rejoin", func(t *testing.T, s store.Store) {
		ctx := context.Background()
		a, b, c := seedGuilds(t, s)
		if _, err := s.MarkGuildsLeftExcept(ctx, []snowflake.ID{a, b}); err != nil {
			t.Fatal(err)
		}

		if err := s.UpsertGuilds(ctx, []store.Guild{
			{ID: c, JoinedAt: time.Now().UTC()},
		}); err != nil {
			t.Fatalf("re-upsert: %v", err)
		}
		left, err := s.MarkGuildsLeftExcept(ctx, []snowflake.ID{a, b, c})
		if err != nil {
			t.Fatalf("MarkGuildsLeftExcept: %v", err)
		}
		if len(left) != 0 {
			t.Errorf("a re-joined guild is present again, got %v", left)
		}
	})
}

func TestMarkGuildLeftIsTheLivePath(t *testing.T) {
	run(t, "live-leave", func(t *testing.T, s store.Store) {
		ctx := context.Background()
		a, b, c := seedGuilds(t, s)

		if err := s.MarkGuildLeft(ctx, b); err != nil {
			t.Fatalf("MarkGuildLeft: %v", err)
		}
		left, err := s.MarkGuildsLeftExcept(ctx, []snowflake.ID{a, c})
		if err != nil {
			t.Fatal(err)
		}
		if len(left) != 0 {
			t.Errorf("b was already marked left, got %v", left)
		}
	})
}

func TestSeedSettingsDoesNotOverwrite(t *testing.T) {
	run(t, "seed", func(t *testing.T, s store.Store) {
		ctx := context.Background()
		configured, fresh := guildID(t), guildID(t)
		now := time.Now().UTC()

		if err := s.UpsertGuilds(ctx, []store.Guild{
			{ID: configured, JoinedAt: now},
			{ID: fresh, JoinedAt: now},
		}); err != nil {
			t.Fatal(err)
		}
		// This guild has already chosen its own chance; seeding must leave it alone.
		if err := s.UpsertSettings(ctx, store.Settings{Guild: configured, Chance: 90, TZ: new(tzBrussels)}); err != nil {
			t.Fatal(err)
		}

		if _, err := s.SeedSettings(ctx, 25); err != nil {
			t.Fatalf("SeedSettings: %v", err)
		}
		if got := find(t, s, configured).Chance; got != 90 {
			t.Errorf("seeding overwrote an existing row: chance = %d, want 90", got)
		}
		if got := find(t, s, fresh).Chance; got != 25 {
			t.Errorf("fresh guild chance = %d, want the seeded 25", got)
		}

		// SeedSettingsFor is the join-time path and has the same do-not-overwrite rule.
		if err := s.SeedSettingsFor(ctx, configured, 3); err != nil {
			t.Fatal(err)
		}
		if got := find(t, s, configured).Chance; got != 90 {
			t.Errorf("SeedSettingsFor overwrote an existing row: chance = %d, want 90", got)
		}

		joined := guildID(t)
		if err := s.SeedSettingsFor(ctx, joined, 11); err != nil {
			t.Fatal(err)
		}
		if got := find(t, s, joined).Chance; got != 11 {
			t.Errorf("newly joined guild chance = %d, want 11", got)
		}
	})
}

// A seeded row has no zone yet: the store cannot see a locale, so settings.FillZones writes one after.
func TestSeededSettingsHaveNoZone(t *testing.T) {
	run(t, "seed-tz", func(t *testing.T, s store.Store) {
		g := guildID(t)
		if err := s.SeedSettingsFor(context.Background(), g, 5); err != nil {
			t.Fatal(err)
		}
		if got := find(t, s, g).TZ; got != nil {
			t.Errorf("seeded tz = %q, want none", *got)
		}
	})
}

func TestWriteMiscAndEmptyBatches(t *testing.T) {
	run(t, "misc", func(t *testing.T, s store.Store) {
		ctx := context.Background()
		u := snowflake.ID(333333333333333333)
		g1, g2 := guildID(t), guildID(t)
		if err := s.WriteMisc(ctx, []store.Misc{
			{At: time.Now().UTC(), Kind: "command", Guild: &g1, User: &u, Data: []byte(`{"name":"play"}`)},
			{At: time.Now().UTC(), Kind: "guild_join", Guild: &g2},
			// A global event has no guild at all; the column has to take a null.
			{At: time.Now().UTC(), Kind: "sound_added", Data: []byte(`{"name":"airhorn"}`)},
		}); err != nil {
			t.Fatalf("WriteMisc: %v", err)
		}
		// Empty batches are a normal flush outcome and must be a no-op, not an error.
		if err := s.WritePlays(ctx, nil); err != nil {
			t.Errorf("WritePlays(nil): %v", err)
		}
		if err := s.WriteMisc(ctx, nil); err != nil {
			t.Errorf("WriteMisc(nil): %v", err)
		}
		if err := s.UpsertGuilds(ctx, nil); err != nil {
			t.Errorf("UpsertGuilds(nil): %v", err)
		}
	})
}

func TestUnknownBoardIsAnError(t *testing.T) {
	run(t, "bad-board", func(t *testing.T, s store.Store) {
		if _, err := s.Leaderboard(context.Background(), guildID(t), "nonsense", 30); err == nil {
			t.Error("an unknown board name should be an error")
		}
	})
}

// optOut returns the backend's row for user, if it still lists one. Expired rows are filtered by
// the query rather than swept, so "still lists" is the whole contract a timed opt-out rests on.
func optOut(t *testing.T, s store.Store, user snowflake.ID) (store.OptOut, bool) {
	t.Helper()
	rows, err := s.ListOptOuts(context.Background())
	if err != nil {
		t.Fatalf("list opt-outs: %v", err)
	}
	for _, r := range rows {
		if r.User == user {
			return r, true
		}
	}
	return store.OptOut{}, false
}

// has reports whether user is in the backend's opt-out list.
func has(t *testing.T, s store.Store, user snowflake.ID) bool {
	t.Helper()
	_, ok := optOut(t, s, user)
	return ok
}

// The opt-out table is the one piece of per-user state the bot keeps, and both backends have to
// agree on it: setting twice is not an error, clearing something absent is not an error, and a
// clear leaves everyone else alone.
func TestOptOutRoundTrip(t *testing.T) {
	run(t, "optouts", func(t *testing.T, s store.Store) {
		ctx := context.Background()
		user, other := userID(t), userID(t)

		if has(t, s, user) {
			t.Fatalf("%s is opted out before anything was written", user)
		}

		if err := s.SetOptOut(ctx, store.OptOut{User: user}); err != nil {
			t.Fatalf("set: %v", err)
		}
		if !has(t, s, user) {
			t.Errorf("%s is not opted out after set", user)
		}

		// Running /optout on twice is an ordinary thing for someone to do, so the second write has
		// to be a no-op rather than a primary key violation.
		if err := s.SetOptOut(ctx, store.OptOut{User: user}); err != nil {
			t.Errorf("setting twice: %v", err)
		}
		if !has(t, s, user) {
			t.Errorf("%s stopped being opted out after a second set", user)
		}

		if err := s.SetOptOut(ctx, store.OptOut{User: other}); err != nil {
			t.Fatalf("set other: %v", err)
		}
		if err := s.ClearOptOut(ctx, user); err != nil {
			t.Fatalf("clear: %v", err)
		}
		if has(t, s, user) {
			t.Errorf("%s is still opted out after clear", user)
		}
		if !has(t, s, other) {
			t.Errorf("clearing %s also cleared %s", user, other)
		}
	})
}

// A timed opt-out has to round-trip its deadline, stop being listed once it has passed, and give
// way to a later write. Load is the only read of this table, so a row that outlives its deadline
// here outlives it everywhere.
func TestTimedOptOut(t *testing.T) {
	run(t, "optouts-timed", func(t *testing.T, s store.Store) {
		ctx := context.Background()
		live, done := userID(t), userID(t)
		until, past := time.Now().UTC().Add(time.Hour), time.Now().UTC().Add(-time.Hour)

		if err := s.SetOptOut(ctx, store.OptOut{User: live, Until: &until}); err != nil {
			t.Fatalf("set timed: %v", err)
		}
		if err := s.SetOptOut(ctx, store.OptOut{User: done, Until: &past}); err != nil {
			t.Fatalf("set expired: %v", err)
		}

		row, ok := optOut(t, s, live)
		if !ok {
			t.Fatalf("%s is missing although the deadline is an hour away", live)
		}
		if row.Until == nil {
			t.Fatalf("%s came back with no deadline, want %v", live, until)
		}
		// Postgres keeps a timestamptz and sqlite RFC3339Nano text, so the contract is the instant.
		if row.Until.Unix() != until.Unix() {
			t.Errorf("deadline = %v, want %v", row.Until.UTC(), until)
		}
		if has(t, s, done) {
			t.Errorf("%s is listed although the opt-out ran out an hour ago", done)
		}

		// Opting out again is how a deadline is dropped. The do-nothing insert this used to be
		// would have kept the old one and quietly ended an opt-out the user made permanent.
		if err := s.SetOptOut(ctx, store.OptOut{User: live}); err != nil {
			t.Fatalf("set indefinite: %v", err)
		}
		row, ok = optOut(t, s, live)
		if !ok {
			t.Fatalf("%s is missing after being opted out indefinitely", live)
		}
		if row.Until != nil {
			t.Errorf("deadline = %v, want none", row.Until.UTC())
		}
	})
}

// A recurring opt-out stores a rule and a window instead of a deadline. Both have to come back
// unchanged: the rule text carries its own DTSTART and TZID, so a backend that mangles it loses
// the time zone the schedule was written in.
func TestRecurringOptOut(t *testing.T) {
	run(t, "optouts-rrule", func(t *testing.T, s store.Store) {
		ctx := context.Background()
		user := userID(t)
		const rule = "DTSTART;TZID=Europe/Brussels:20260922T000000\nRRULE:FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR;BYHOUR=9"

		if err := s.SetOptOut(ctx, store.OptOut{User: user, Rule: rule, Window: 8 * time.Hour}); err != nil {
			t.Fatalf("set recurring: %v", err)
		}
		row, ok := optOut(t, s, user)
		if !ok {
			t.Fatalf("%s is missing: a rule row has no deadline to expire", user)
		}
		if row.Rule != rule {
			t.Errorf("rule = %q, want %q", row.Rule, rule)
		}
		if row.Window != 8*time.Hour {
			t.Errorf("window = %v, want 8h", row.Window)
		}
		if row.Until != nil {
			t.Errorf("until = %v, want none on a recurring row", row.Until)
		}

		// Switching back to a plain opt-out has to clear the schedule, or the row would carry both.
		if err := s.SetOptOut(ctx, store.OptOut{User: user}); err != nil {
			t.Fatalf("set indefinite: %v", err)
		}
		row, _ = optOut(t, s, user)
		if row.Rule != "" || row.Window != 0 {
			t.Errorf("schedule survived a plain opt-out: %+v", row)
		}
	})
}

// Clearing someone who never opted out is what /optout off does for most people who run it.
func TestClearOptOutIsNotAnErrorWhenAbsent(t *testing.T) {
	run(t, "optouts-absent", func(t *testing.T, s store.Store) {
		if err := s.ClearOptOut(context.Background(), userID(t)); err != nil {
			t.Errorf("clearing an absent opt-out: %v", err)
		}
	})
}

func chaosFor(t *testing.T, s store.Store, g snowflake.ID) (store.Chaos, bool) {
	t.Helper()
	rows, err := s.ListChaos(context.Background())
	if err != nil {
		t.Fatalf("list chaos: %v", err)
	}
	for _, r := range rows {
		if r.Guild == g {
			return r, true
		}
	}
	return store.Chaos{}, false
}

// The table is append-only, so the newest row per guild is the whole contract: an older window
// must never come back, and an off must hide every window before it.
func TestChaosLatestRowWins(t *testing.T) {
	const (
		ruleA = "DTSTART;TZID=Europe/Brussels:20260922T000000\nRRULE:FREQ=WEEKLY;BYDAY=FR;BYHOUR=20"
		ruleB = "DTSTART;TZID=Europe/Brussels:20260922T000000\nRRULE:FREQ=WEEKLY;BYDAY=SA,SU;BYHOUR=14"
	)
	a := store.Chaos{Rule: ruleA, Hours: 3, Chance: 40}
	b := store.Chaos{Rule: ruleB, Hours: 5, Chance: 60}
	off := store.Chaos{}
	cases := []struct {
		name   string
		rows   []store.Chaos
		want   store.Chaos
		wantOn bool
	}{
		{"one window", []store.Chaos{a}, a, true},
		{"the newer window", []store.Chaos{a, b}, b, true},
		{"off after a window", []store.Chaos{a, off}, off, false},
		{"a window after off", []store.Chaos{a, off, b}, b, true},
	}
	for _, c := range cases {
		run(t, "chaos/"+c.name, func(t *testing.T, s store.Store) {
			ctx := context.Background()
			g, other := guildID(t), guildID(t)
			if err := s.AppendChaos(ctx, store.Chaos{Guild: other, Rule: ruleA, Hours: 1, Chance: 90, CreatedBy: 1}); err != nil {
				t.Fatalf("append for another guild: %v", err)
			}
			for _, r := range c.rows {
				r.Guild, r.CreatedBy = g, 1
				if err := s.AppendChaos(ctx, r); err != nil {
					t.Fatalf("append %+v: %v", r, err)
				}
			}
			got, on := chaosFor(t, s, g)
			want := c.want
			want.Guild = g
			if on != c.wantOn || (on && got != want) {
				t.Errorf("got %+v (on %v), want %+v (on %v)", got, on, want, c.wantOn)
			}
			if _, ok := chaosFor(t, s, other); !ok {
				t.Error("another guild's window went missing")
			}
		})
	}
}

// A nil guild is every guild, for /stats user scope:global.
func TestUserStatsAndHeardSoundsAcrossGuilds(t *testing.T) {
	run(t, "user-stats-global", func(t *testing.T, s store.Store) {
		ctx := context.Background()
		g1, g2 := guildID(t), guildID(t)
		me := userID(t)
		now := time.Now().UTC()
		if err := s.WritePlays(ctx, []store.Play{
			{At: now.Add(-2 * time.Minute), Guild: g1, Channel: g1 + 1, Sound: "a", Trigger: trigLoop,
				ListenerIDs: []snowflake.ID{me}, OK: true, Duration: time.Second},
			{At: now.Add(-time.Minute), Guild: g2, Channel: g2 + 1, Sound: "b", Trigger: trigEncore,
				ListenerIDs: []snowflake.ID{me}, FledIDs: []snowflake.ID{me}, OK: true, Duration: time.Second},
			{At: now, Guild: g2, Channel: g2 + 1, Sound: "c", Trigger: trigCmd, User: &me,
				ListenerIDs: []snowflake.ID{me}, OK: true, Duration: time.Second},
		}); err != nil {
			t.Fatalf("WritePlays: %v", err)
		}

		tests := []struct {
			name                   string
			guild                  *snowflake.ID
			heard, triggered, fled int
			sounds                 []string
		}{
			{"one guild", &g1, 1, 0, 0, []string{"a"}},
			{"the other", &g2, 2, 1, 1, []string{"b"}},
			{"every guild", nil, 3, 1, 1, []string{"a", "b"}},
		}
		for _, tt := range tests {
			st, err := s.UserStats(ctx, tt.guild, me)
			if err != nil {
				t.Fatalf("%s: UserStats: %v", tt.name, err)
			}
			if st.Heard != tt.heard || st.Triggered != tt.triggered || st.Fled != tt.fled {
				t.Errorf("%s: heard/triggered/fled = %d/%d/%d, want %d/%d/%d",
					tt.name, st.Heard, st.Triggered, st.Fled, tt.heard, tt.triggered, tt.fled)
			}
			got, err := s.HeardSounds(ctx, tt.guild, me, []string{trigLoop, trigEncore})
			if err != nil {
				t.Fatalf("%s: HeardSounds: %v", tt.name, err)
			}
			slices.Sort(got)
			if !slices.Equal(got, tt.sounds) {
				t.Errorf("%s: HeardSounds = %v, want %v", tt.name, got, tt.sounds)
			}
		}
	})
}

// The within-guild rank is exact: ties sit level, and only people caught in the window count.
func TestUserRank(t *testing.T) {
	run(t, "user-rank", func(t *testing.T, s store.Store) {
		ctx := context.Background()
		g := guildID(t)
		a, b, c, d := userID(t), userID(t), userID(t), userID(t)
		now := time.Now().UTC()
		play := func(ago time.Duration, trigger string, by *snowflake.ID, ok bool, who, fled []snowflake.ID) store.Play {
			return store.Play{At: now.Add(-ago), Guild: g, Channel: g + 1, Sound: "a", Trigger: trigger, User: by,
				ListenerIDs: who, FledIDs: fled, OK: ok, Duration: time.Second}
		}
		stale := 40 * 24 * time.Hour
		if err := s.WritePlays(ctx, []store.Play{
			play(time.Minute, trigLoop, nil, true, []snowflake.ID{a, b, c}, []snowflake.ID{c}),
			play(2*time.Minute, trigLoop, nil, true, []snowflake.ID{a, b}, nil),
			play(3*time.Minute, trigLoop, nil, true, []snowflake.ID{a}, nil),
			play(4*time.Minute, trigCmd, &b, true, []snowflake.ID{b}, nil),
			play(5*time.Minute, trigLoop, nil, false, []snowflake.ID{d}, []snowflake.ID{d}),
			play(stale, trigLoop, nil, true, []snowflake.ID{c}, nil),
			play(stale, trigLoop, nil, true, []snowflake.ID{c}, nil),
			play(stale, trigLoop, nil, true, []snowflake.ID{c}, nil),
		}); err != nil {
			t.Fatalf("WritePlays: %v", err)
		}
		since := now.Add(-30 * 24 * time.Hour)
		tests := []struct {
			name  string
			guild snowflake.ID
			user  snowflake.ID
			want  store.UserRank
		}{
			{"tied at the top", g, a, store.UserRank{UserCounts: store.UserCounts{Heard: 3}, HeardBelow: 1, Of: 3}},
			{"the one who asked", g, b, store.UserRank{UserCounts: store.UserCounts{Heard: 3, Triggered: 1}, HeardBelow: 1, TriggeredBelow: 2, Of: 3}},
			{"stale plays do not count", g, c, store.UserRank{UserCounts: store.UserCounts{Heard: 1, Fled: 1}, FledBelow: 2, Of: 3}},
			{"fled but never caught", g, d, store.UserRank{}},
			{"empty guild", guildID(t), a, store.UserRank{}},
		}
		for _, tt := range tests {
			got, err := s.UserRank(ctx, tt.guild, tt.user, since)
			if err != nil {
				t.Fatalf("%s: UserRank: %v", tt.name, err)
			}
			if got != tt.want {
				t.Errorf("%s: UserRank = %+v, want %+v", tt.name, got, tt.want)
			}
		}
	})
}

func TestUserRecent(t *testing.T) {
	run(t, "user-recent", func(t *testing.T, s store.Store) {
		ctx := context.Background()
		g1, g2 := guildID(t), guildID(t)
		me := userID(t)
		now := time.Now().UTC()
		if err := s.WritePlays(ctx, []store.Play{
			{At: now.Add(-time.Minute), Guild: g1, Channel: g1 + 1, Sound: "a", Trigger: trigLoop,
				ListenerIDs: []snowflake.ID{me}, OK: true, Duration: time.Second},
			{At: now.Add(-2 * time.Minute), Guild: g2, Channel: g2 + 1, Sound: "a", Trigger: trigCmd, User: &me,
				ListenerIDs: []snowflake.ID{me}, OK: true, Duration: time.Second},
			{At: now.Add(-3 * time.Minute), Guild: g2, Channel: g2 + 1, Sound: "a", Trigger: trigLoop,
				ListenerIDs: []snowflake.ID{me}, FledIDs: []snowflake.ID{me}, OK: false, Reason: reasonGone, Duration: time.Second},
			{At: now.Add(-40 * 24 * time.Hour), Guild: g1, Channel: g1 + 1, Sound: "a", Trigger: trigCmd, User: &me,
				ListenerIDs: []snowflake.ID{me}, OK: true, Duration: time.Second},
		}); err != nil {
			t.Fatalf("WritePlays: %v", err)
		}
		since := now.Add(-30 * 24 * time.Hour)
		got, err := s.UserRecent(ctx, me, since)
		if err != nil {
			t.Fatalf("UserRecent: %v", err)
		}
		if want := (store.UserCounts{Heard: 2, Triggered: 1, Fled: 1}); got != want {
			t.Errorf("UserRecent = %+v, want %+v", got, want)
		}
		if got, err := s.UserRecent(ctx, userID(t), since); err != nil || got != (store.UserCounts{}) {
			t.Errorf("UserRecent for nobody = %+v, %v; want zeros", got, err)
		}
	})
}

func TestGuildRecent(t *testing.T) {
	run(t, "guild-recent", func(t *testing.T, s store.Store) {
		ctx := context.Background()
		g := guildID(t)
		now := time.Now().UTC()
		play := func(ago time.Duration, ok bool, listeners int) store.Play {
			who := make([]snowflake.ID, listeners)
			for i := range who {
				who[i] = snowflake.ID(i + 1)
			}
			return store.Play{At: now.Add(-ago), Guild: g, Channel: g + 1, Sound: "a", Trigger: trigLoop,
				ListenerIDs: who, OK: ok, Duration: time.Second}
		}
		if err := s.WritePlays(ctx, []store.Play{
			play(time.Minute, true, 2),
			play(2*time.Minute, true, 4),
			play(3*time.Minute, false, 9),
			play(40*24*time.Hour, true, 9),
		}); err != nil {
			t.Fatalf("WritePlays: %v", err)
		}
		since := now.Add(-30 * 24 * time.Hour)
		got, err := s.GuildRecent(ctx, g, since)
		if err != nil {
			t.Fatalf("GuildRecent: %v", err)
		}
		if want := (store.GuildRecent{Plays: 2, AvgListeners: 3}); got != want {
			t.Errorf("GuildRecent = %+v, want %+v", got, want)
		}
		if got, err := s.GuildRecent(ctx, guildID(t), since); err != nil || got != (store.GuildRecent{}) {
			t.Errorf("GuildRecent for an empty guild = %+v, %v; want zeros", got, err)
		}
	})
}

// cutsOf is what Cuts promises for a sorted population: cut k is the value at position ceil(k*N/100).
func cutsOf(sorted ...float64) []float64 {
	out := make([]float64, 100)
	for k := 1; k <= 100; k++ {
		out[k-1] = sorted[(k*len(sorted)+99)/100-1]
	}
	return out
}

// Cuts reads every guild, so each call gets an hour of its own, far from the plays other tests
// write at now, and a shared postgres cannot leak into it.
func cutWindow(t *testing.T) (since, until time.Time) {
	t.Helper()
	since = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(guildID(t)%1e6) * time.Hour)
	return since, since.Add(time.Hour)
}

func TestCuts(t *testing.T) {
	run(t, "cuts", func(t *testing.T, s store.Store) {
		ctx := context.Background()
		g1, g2 := guildID(t), guildID(t)
		a, b, c, d, e := userID(t), userID(t), userID(t), userID(t), userID(t)
		since, until := cutWindow(t)
		play := func(at time.Time, g snowflake.ID, trigger string, by *snowflake.ID, ok bool, who, fled []snowflake.ID) store.Play {
			return store.Play{At: at, Guild: g, Channel: g + 1, Sound: "a", Trigger: trigger, User: by,
				ListenerIDs: who, FledIDs: fled, OK: ok, Duration: time.Second}
		}
		in := since.Add(10 * time.Minute)
		if err := s.WritePlays(ctx, []store.Play{
			play(in, g1, trigLoop, nil, true, []snowflake.ID{a, b, c, d}, []snowflake.ID{d}),
			play(in, g2, trigLoop, nil, true, []snowflake.ID{d}, nil),
			play(in, g1, trigCmd, &a, true, nil, nil),
			play(in, g1, trigCmd, &a, true, nil, nil),
			play(in, g1, trigLoop, nil, false, []snowflake.ID{e}, []snowflake.ID{e}),
			play(in, g1, trigCmd, &e, true, nil, nil),
			play(since.Add(-time.Minute), g1, trigLoop, nil, true, []snowflake.ID{a}, nil),
			play(until, g1, trigLoop, nil, true, []snowflake.ID{b}, nil),
		}); err != nil {
			t.Fatalf("WritePlays: %v", err)
		}

		tests := []struct {
			metric string
			want   []float64
		}{
			{"heard", cutsOf(1, 1, 1, 2)},
			{"triggered", cutsOf(0, 0, 0, 2)}, // e ran /play but was never caught
			{"fled", cutsOf(0, 0, 0, 1)},      // e fled an empty room and was never caught
			{"plays", cutsOf(1, 4)},
			{"listeners", cutsOf(1, 1)},
		}
		empty, _ := cutWindow(t)
		for _, tt := range tests {
			got, err := s.Cuts(ctx, tt.metric, since, until)
			if err != nil {
				t.Fatalf("Cuts(%s): %v", tt.metric, err)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("Cuts(%s) = %v, want %v", tt.metric, got, tt.want)
			}
			got, err = s.Cuts(ctx, tt.metric, empty, empty.Add(time.Hour))
			if err != nil || len(got) != 0 {
				t.Errorf("Cuts(%s) on an empty window = %v, %v; want none", tt.metric, got, err)
			}
		}
		if _, err := s.Cuts(ctx, "nope", since, until); err == nil {
			t.Error("an unknown metric is not an error")
		}
	})
}

// The series has to read the same before and after a refresh: on postgres the older hours move from
// the live tail into the rollup, and nothing may be counted twice or dropped on the way.
func TestHourlySeries(t *testing.T) {
	run(t, "hourly", func(t *testing.T, s store.Store) {
		ctx := context.Background()
		g, a, b := guildID(t), userID(t), userID(t)
		now := time.Now().UTC()
		cur := now.Truncate(time.Hour)
		base := cur.Add(-2 * time.Hour)
		play := func(at time.Time, trigger string, by *snowflake.ID, ok bool, reason string, who, fled []snowflake.ID) store.Play {
			return store.Play{At: at, Guild: g, Channel: g + 1, Sound: "a", Trigger: trigger, User: by,
				ListenerIDs: who, FledIDs: fled, OK: ok, Reason: reason, Duration: time.Second}
		}
		if err := s.WritePlays(ctx, []store.Play{
			play(base.Add(-time.Hour), trigLoop, nil, true, "", []snowflake.ID{a}, nil), // before since
			play(base.Add(10*time.Minute), trigLoop, nil, true, "", []snowflake.ID{a, b}, []snowflake.ID{b}),
			play(base.Add(20*time.Minute), trigCmd, &a, true, "", []snowflake.ID{a}, nil),
			play(base.Add(65*time.Minute), trigLoop, nil, false, reasonFake, []snowflake.ID{a}, nil),
			play(base.Add(66*time.Minute), trigLoop, nil, false, reasonFail, nil, nil),
			play(now, trigEncore, nil, true, "", []snowflake.ID{a}, nil),
		}); err != nil {
			t.Fatalf("WritePlays: %v", err)
		}

		wantPlays := []store.PlayHour{
			{Hour: base, Plays: 2, Loops: 1, Commands: 1, Listeners: 3, Guilds: 1},
			{Hour: base.Add(time.Hour), Failed: 1, FakeOuts: 1},
			{Hour: cur, Plays: 1, Encores: 1, Listeners: 1, Guilds: 1},
		}
		wantA := []store.UserHour{{Hour: base, Heard: 2, Triggered: 1}, {Hour: cur, Heard: 1}}
		wantB := []store.UserHour{{Hour: base, Heard: 1, Fled: 1}}

		check := func(when string) {
			t.Helper()
			ph, err := s.PlaysHourly(ctx, &g, base)
			expectSeries(t, "PlaysHourly "+when, ph, err, wantPlays, samePlayHour)
			ua, err := s.UserHourly(ctx, &g, a, base)
			expectSeries(t, "UserHourly(a) "+when, ua, err, wantA, sameUserHour)
			// A nil guild is every guild; b only ever heard this one.
			ub, err := s.UserHourly(ctx, nil, b, base)
			expectSeries(t, "UserHourly(b) "+when, ub, err, wantB, sameUserHour)
		}
		check("before refresh")
		if err := s.RefreshAnalytics(ctx); err != nil {
			t.Fatalf("RefreshAnalytics: %v", err)
		}
		check("after refresh")

		// Bot-wide reads every guild, so all this can say on a shared database is that it answers.
		if _, err := s.PlaysHourly(ctx, nil, cur); err != nil {
			t.Errorf("PlaysHourly(nil): %v", err)
		}
	})
}

func expectSeries[T any](t *testing.T, what string, got []T, err error, want []T, eq func(T, T) bool) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", what, err)
	}
	if !slices.EqualFunc(got, want, eq) {
		t.Errorf("%s = %+v, want %+v", what, got, want)
	}
}

// samePlayHour compares on the instant, not the zone the Hour was read back in.
func samePlayHour(x, y store.PlayHour) bool {
	if !x.Hour.Equal(y.Hour) {
		return false
	}
	x.Hour = y.Hour
	return x == y
}

func sameUserHour(x, y store.UserHour) bool {
	return x.Hour.Equal(y.Hour) && x.Heard == y.Heard && x.Fled == y.Fled && x.Triggered == y.Triggered
}
