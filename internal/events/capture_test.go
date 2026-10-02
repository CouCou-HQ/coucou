package events

import (
	"context"
	"sync"
	"time"

	"github.com/disgoorg/snowflake/v2"

	"github.com/be-sandaa/coucou/internal/store"
)

// capture is a store.Store that only remembers what was written to the events table. Everything
// else exists to satisfy the interface.
type capture struct {
	mu   sync.Mutex
	misc []store.Misc
}

func (c *capture) WriteMisc(_ context.Context, m []store.Misc) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.misc = append(c.misc, m...)
	return nil
}

func (c *capture) rows() []store.Misc {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.misc
}

func (*capture) Migrate(context.Context) error                          { return nil }
func (*capture) Ping(context.Context) error                             { return nil }
func (*capture) Close()                                                 {}
func (*capture) ListSettings(context.Context) ([]store.Settings, error) { return nil, nil }
func (*capture) UpsertSettings(context.Context, store.Settings) error   { return nil }
func (*capture) WritePlays(context.Context, []store.Play) error         { return nil }
func (*capture) UpsertGuilds(context.Context, []store.Guild) error      { return nil }
func (*capture) MarkGuildLeft(context.Context, snowflake.ID) error      { return nil }
func (*capture) SeedSettings(context.Context, int) (int64, error)       { return 0, nil }
func (*capture) TopGuilds(context.Context, int) ([]store.Row, error)    { return nil, nil }
func (*capture) UserRank(context.Context, snowflake.ID, snowflake.ID, time.Time) (store.UserRank, error) {
	return store.UserRank{}, nil
}
func (*capture) UserRecent(context.Context, snowflake.ID, time.Time) (store.UserCounts, error) {
	return store.UserCounts{}, nil
}
func (*capture) GuildRecent(context.Context, snowflake.ID, time.Time) (store.GuildRecent, error) {
	return store.GuildRecent{}, nil
}
func (*capture) Cuts(context.Context, string, time.Time, time.Time) ([]float64, error) {
	return nil, nil
}
func (*capture) GlobalStats(context.Context) (store.GlobalStats, error) {
	return store.GlobalStats{}, nil
}
func (*capture) SeedSettingsFor(context.Context, snowflake.ID, int) error { return nil }

func (*capture) MarkGuildsLeftExcept(context.Context, []snowflake.ID) ([]snowflake.ID, error) {
	return nil, nil
}

func (*capture) GuildStats(context.Context, snowflake.ID) (store.GuildStats, error) {
	return store.GuildStats{}, nil
}

func (*capture) UserStats(context.Context, *snowflake.ID, snowflake.ID) (store.UserStats, error) {
	return store.UserStats{}, nil
}

func (*capture) HeardSounds(context.Context, *snowflake.ID, snowflake.ID, []string) ([]string, error) {
	return nil, nil
}

func (*capture) Leaderboard(context.Context, snowflake.ID, string, int) ([]store.Row, error) {
	return nil, nil
}

func (c *capture) ListOptOuts(context.Context) ([]store.OptOut, error) { return nil, nil }
func (c *capture) SetOptOut(context.Context, store.OptOut) error       { return nil }
func (c *capture) ClearOptOut(context.Context, snowflake.ID) error     { return nil }

func (c *capture) ListChaos(context.Context) ([]store.Chaos, error) { return nil, nil }
func (c *capture) AppendChaos(context.Context, store.Chaos) error   { return nil }

func (*capture) PlaysHourly(context.Context, *snowflake.ID, time.Time) ([]store.PlayHour, error) {
	return nil, nil
}
func (*capture) UserHourly(context.Context, *snowflake.ID, snowflake.ID, time.Time) ([]store.UserHour, error) {
	return nil, nil
}
func (*capture) RefreshAnalytics(context.Context) error { return nil }
