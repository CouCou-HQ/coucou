package handlers

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"

	"github.com/be-sandaa/coucou/internal/settings"
	"github.com/be-sandaa/coucou/internal/store"
)

const testGuild = snowflake.ID(42)

var (
	errUpsert   = errors.New("upsert guilds failed")
	errSeed     = errors.New("seed settings failed")
	errSettings = errors.New("upsert settings failed")
	errMarkLeft = errors.New("mark guild left failed")
)

// fakeStore records what the sync handlers wrote and can fail any one step. Hand-written because
// four of the sixteen methods are load-bearing here and the rest only exist to satisfy the
// interface — a generated mock would be larger and read worse at the call site.
type fakeStore struct {
	upserted []store.Guild
	seeded   []snowflake.ID
	settings int     // UpsertSettings calls, which is how settings.Store.Update shows up from here
	tz       *string // the zone the last of them wrote
	left     []snowflake.ID

	failUpsert, failSeed, failSettings, failMarkLeft error
}

func (f *fakeStore) UpsertGuilds(_ context.Context, g []store.Guild) error {
	f.upserted = append(f.upserted, g...)
	return f.failUpsert
}

func (f *fakeStore) SeedSettingsFor(_ context.Context, g snowflake.ID, _ store.Defaults) error {
	f.seeded = append(f.seeded, g)
	return f.failSeed
}

func (f *fakeStore) UpsertSettings(_ context.Context, st store.Settings) error {
	f.settings++
	f.tz = st.TZ
	return f.failSettings
}

func (f *fakeStore) MarkGuildLeft(_ context.Context, g snowflake.ID) error {
	f.left = append(f.left, g)
	return f.failMarkLeft
}

func (f *fakeStore) Migrate(context.Context) error                               { return nil }
func (f *fakeStore) Ping(context.Context) error                                  { return nil }
func (f *fakeStore) Close()                                                      {}
func (f *fakeStore) ListSettings(context.Context) ([]store.Settings, error)      { return nil, nil }
func (f *fakeStore) WritePlays(context.Context, []store.Play) error              { return nil }
func (f *fakeStore) WriteMisc(context.Context, []store.Misc) error               { return nil }
func (f *fakeStore) SeedSettings(context.Context, store.Defaults) (int64, error) { return 0, nil }

func (f *fakeStore) MarkGuildsLeftExcept(context.Context, []snowflake.ID) ([]snowflake.ID, error) {
	return nil, nil
}

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
func (f *fakeStore) CharacterPlays(context.Context, *snowflake.ID) ([]store.Row, error) {
	return nil, nil
}
func (*fakeStore) Forget(context.Context, snowflake.ID) error { return nil }
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

func (f *fakeStore) ListOptOuts(context.Context) ([]store.Silence, error) { return nil, nil }
func (f *fakeStore) SetOptOut(context.Context, store.Silence) error       { return nil }
func (f *fakeStore) ClearOptOut(context.Context, snowflake.ID) error      { return nil }
func (f *fakeStore) ListQuiet(context.Context) ([]store.Silence, error)   { return nil, nil }
func (f *fakeStore) SetQuiet(context.Context, store.Silence) error        { return nil }
func (f *fakeStore) ClearQuiet(context.Context, snowflake.ID) error       { return nil }

// A join writes the guild row, seeds its settings, and fills in a zone from its locale. The
// defaults are only written when there is something non-zero to write; the zone always is, for a
// new guild.
func TestGuildJoinedSyncWrites(t *testing.T) {
	tests := []struct {
		name         string
		defaults     store.Defaults
		wantSettings int
	}{
		{"seeded defaults and a zone", store.Defaults{Chance: 5}, 2},
		{"zero defaults, only the zone", store.Defaults{}, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := &fakeStore{}
			joined := time.Now().UTC().Truncate(time.Second)

			h := GuildJoinedSync(db, settings.New(db), tt.defaults)
			if err := h(t.Context(), gatewayGuild(testGuild, joined)); err != nil {
				t.Fatalf("handler returned %v, want nil", err)
			}

			if len(db.upserted) != 1 || db.upserted[0].ID != testGuild || !db.upserted[0].JoinedAt.Equal(joined) {
				t.Errorf("upserted %+v, want one row for %s joined at %v", db.upserted, testGuild, joined)
			}
			if len(db.seeded) != 1 || db.seeded[0] != testGuild {
				t.Errorf("seeded %v, want exactly %s", db.seeded, testGuild)
			}
			if db.settings != tt.wantSettings {
				t.Errorf("settings writes = %d, want %d", db.settings, tt.wantSettings)
			}
		})
	}
}

// The zone a new guild gets is the one its locale points at, written, not worked out later.
func TestGuildJoinedSyncFillsTheZone(t *testing.T) {
	db := &fakeStore{}
	if err := GuildJoinedSync(db, settings.New(db), store.Defaults{})(t.Context(), gatewayGuild(testGuild, time.Time{})); err != nil {
		t.Fatal(err)
	}
	if db.tz == nil || *db.tz != "Europe/Paris" {
		t.Errorf("zone written = %v, want the one the French locale points at", db.tz)
	}
}

// A guild coming back with a zone already keeps it.
func TestGuildJoinedSyncKeepsAZone(t *testing.T) {
	const back = snowflake.ID(43)
	db := &fakeStore{}
	set := settings.New(db)
	tz := "Asia/Tokyo"
	if _, err := set.Update(t.Context(), back, 1, func(s *settings.Settings) { s.TZ = &tz }); err != nil {
		t.Fatal(err)
	}
	if err := GuildJoinedSync(db, set, store.Defaults{})(t.Context(), gatewayGuild(back, time.Time{})); err != nil {
		t.Fatal(err)
	}
	if got := settings.Zone(set.Get(back)); got != tz {
		t.Errorf("zone = %q after rejoining, want %q kept", got, tz)
	}
}

// A guild coming back keeps the settings it chose: the seed leaves its row alone, and so must the
// mirror, or the bot would play by the profile's defaults until the next restart reloaded the row.
func TestGuildJoinedSyncKeepsChosenSettings(t *testing.T) {
	const back = snowflake.ID(44)
	db := &fakeStore{}
	set := settings.New(db)
	if _, err := set.Update(t.Context(), back, 1, func(s *settings.Settings) { s.Chance, s.Suspense = 90, 3 }); err != nil {
		t.Fatal(err)
	}
	h := GuildJoinedSync(db, set, store.Defaults{Chance: 5, Suspense: 8, FakeOut: 10, Encore: 5})
	if err := h(t.Context(), gatewayGuild(back, time.Time{})); err != nil {
		t.Fatal(err)
	}
	if got := set.Get(back); got.Chance != 90 || got.Suspense != 3 || got.FakeOut != 0 {
		t.Errorf("settings after rejoining = %+v, want chance 90 and suspense 3 kept", got)
	}
}

// Every step returns its error rather than swallowing it: the middleware retry is what turns a
// transient database failure into a guild that eventually gets its row, and a swallowed error
// would leave the bot running against state it believes it wrote.
func TestGuildJoinedSyncReturnsEachStepsError(t *testing.T) {
	tests := []struct {
		name string
		db   *fakeStore
		want error
	}{
		{"upsert", &fakeStore{failUpsert: errUpsert}, errUpsert},
		{"seed", &fakeStore{failSeed: errSeed}, errSeed},
		{"settings", &fakeStore{failSettings: errSettings}, errSettings},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := GuildJoinedSync(tt.db, settings.New(tt.db), store.Defaults{Chance: 5})
			if err := h(t.Context(), gatewayGuild(testGuild, time.Time{})); !errors.Is(err, tt.want) {
				t.Errorf("handler returned %v, want %v", err, tt.want)
			}
		})
	}
}

// A failing upsert must not go on to seed: retrying the whole handler is what puts the guild row
// and its settings row back in order, and a partial write makes that retry a different operation.
func TestGuildJoinedSyncStopsAtTheFirstFailure(t *testing.T) {
	db := &fakeStore{failUpsert: errUpsert}

	h := GuildJoinedSync(db, settings.New(db), store.Defaults{Chance: 5})
	if err := h(t.Context(), gatewayGuild(testGuild, time.Time{})); !errors.Is(err, errUpsert) {
		t.Fatalf("handler returned %v, want %v", err, errUpsert)
	}

	if len(db.seeded) != 0 {
		t.Errorf("seeded %v after the guild upsert failed, want nothing", db.seeded)
	}
	if db.settings != 0 {
		t.Errorf("wrote settings %d times after the guild upsert failed, want 0", db.settings)
	}
}

// gatewayGuild is the GUILD_CREATE payload the handler now receives, built through the embedded
// RestGuild the fields actually live on.
func gatewayGuild(id snowflake.ID, joined time.Time) *discord.GatewayGuild {
	g := &discord.GatewayGuild{}
	g.ID = id
	g.JoinedAt = joined
	g.PreferredLocale = string(discord.LocaleFrench)
	return g
}

// What reaches this handler is a Discord frame, so every one of them is a live departure. The
// reconciled case it used to branch on is a domain event now and never arrives here.
func TestGuildLeftSyncWritesTheRow(t *testing.T) {
	db := &fakeStore{}
	h := GuildLeftSync(db)
	if err := h(t.Context(), &discord.Guild{ID: testGuild}); err != nil {
		t.Fatalf("handler returned %v, want nil", err)
	}
	if len(db.left) != 1 {
		t.Errorf("marked left %v, want one write", db.left)
	}
}

func TestGuildLeftSyncReturnsTheStoreError(t *testing.T) {
	db := &fakeStore{failMarkLeft: errMarkLeft}
	h := GuildLeftSync(db)
	if err := h(t.Context(), &discord.Guild{ID: testGuild}); !errors.Is(err, errMarkLeft) {
		t.Errorf("handler returned %v, want %v", err, errMarkLeft)
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
