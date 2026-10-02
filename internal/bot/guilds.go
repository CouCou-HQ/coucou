package bot

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/snowflake/v2"

	"github.com/be-sandaa/coucou/internal/bus"
	"github.com/be-sandaa/coucou/internal/settings"
	"github.com/be-sandaa/coucou/internal/store"
)

// cacheSettle bounds the wait for the GUILD_CREATE burst that follows READY. Generous, because
// giving up early costs a partial reconcile, not a corrupted one.
const cacheSettle = 2 * time.Minute

// ReadyTracker counts the shards whose READY has been handled. disgo flips a shard to ready — which
// is what unblocks OpenShardManager — before it dispatches the READY payload that seeds the unready
// guild set, so at open the set is reliably empty and means nothing at all. Counting shards is the
// only signal that tells "the burst has not started" from "the burst is done", and it is right for
// a shard holding no guilds, where the unready set never becomes non-empty in the first place.
type ReadyTracker struct {
	mu   sync.Mutex
	seen map[int]struct{}
}

func NewReadyTracker() *ReadyTracker { return &ReadyTracker{seen: make(map[int]struct{})} }

// Count is the number of distinct shards that have reported. A reconnecting shard re-reports and
// re-adds its guilds to the unready set; the count does not move, and the drain wait below covers it.
func (t *ReadyTracker) Count() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.seen)
}

// OnReady logs the handshake and records the shard. It is registered as the client's READY listener.
func (t *ReadyTracker) OnReady() bot.EventListener {
	return bot.NewListenerFunc(func(e *events.Ready) {
		t.mu.Lock()
		t.seen[e.ShardID()] = struct{}{}
		n := len(t.seen)
		t.mu.Unlock()
		slog.Info("ready", slog.String("user", e.User.Username), slog.Int("shard", e.ShardID()),
			slog.Int("guilds", len(e.Guilds)), slog.Int("shards_ready", n))
	})
}

// SyncGuilds reconciles the guilds table with what the gateway handed us: upsert everything we are
// in, mark anything else as left, seed settings rows, reload settings. The guild set only exists in
// the cache, so the comparison against the database has to happen where the cache is.
//
// It must not run until every shard has reported. MarkGuildsLeftExcept treats anything missing from
// the cache as a guild we were removed from, so running it against one shard's guilds would mark
// every other shard's as left and publish a GuildLeft for each. The caller owns that ordering by
// calling this only once the shard manager has opened; this waits out the guild burst that follows.
//
// Deliberately no longer a GuildsReady listener. disgo dispatches that when the client-wide unready
// set empties, which under sharding can fire before the last shard has identified — and never at
// all for a shard holding no guilds. There is no count of it that is right.
func SyncGuilds(ctx context.Context, c *bot.Client, t *ReadyTracker, b *bus.Bus, db store.Store, set *settings.Store, d store.Defaults) error {
	unready, err := waitForGuilds(ctx, shardCount(c), t.Count, c.Caches.UnreadyGuildIDs)
	if err != nil {
		// Reconciling while a shard is still silent is the one outcome worse than not reconciling:
		// its guilds are in neither cache nor unready set, so they would be marked left.
		slog.Error("guilds: shards never all reported, skipping the reconcile", slog.Any("err", err))
		return nil
	}
	// GuildLeft goes on the bus, and nothing may publish before the router has its subscribers.
	select {
	case <-b.Running():
	case <-ctx.Done():
		return ctx.Err()
	}

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	var present []store.Guild
	var ids []snowflake.ID
	locales := map[snowflake.ID]discord.Locale{}
	for g := range c.Caches.Guilds() {
		present = append(present, store.Guild{ID: g.ID, JoinedAt: g.JoinedAt})
		ids = append(ids, g.ID)
		locales[g.ID] = discord.Locale(g.PreferredLocale)
	}
	if err := db.UpsertGuilds(ctx, present); err != nil {
		slog.Error("guilds: upsert", slog.Any("err", err))
		return nil
	}
	// A guild whose GUILD_CREATE never arrived is unavailable, not departed — Discord leaves guilds
	// that way through an outage. Keeping it out of the candidates lets the rest reconcile instead
	// of two stragglers costing every other guild its row.
	ids = append(ids, unready...)
	left, err := db.MarkGuildsLeftExcept(ctx, ids)
	if err != nil {
		slog.Error("guilds: mark left", slog.Any("err", err))
	}
	for _, id := range left {
		b.Publish(ctx, bus.GuildLeft{Guild: id, Reconciled: true})
	}
	seeded, err := db.SeedSettings(ctx, d)
	if err != nil {
		slog.Error("guilds: seed settings", slog.Any("err", err))
	}
	if err := set.Load(ctx); err != nil {
		slog.Error("settings: reload after sync", slog.Any("err", err))
	}
	// After the reload, so it sees which guilds already have a zone. This is the one place every
	// guild the bot is in passes through — including ones joined while it was down, which get no
	// GUILD_CREATE of their own — so it is also the backfill for rows seeded before zones were filled.
	zoned := fillZones(ctx, set, locales)
	slog.Info("guilds synced", slog.Int("present", len(present)),
		slog.Int("left_while_down", len(left)), slog.Int("unready", len(unready)),
		slog.Int64("settings_seeded", seeded), slog.Int("zones_filled", zoned))
	return nil
}

// fillZones logs rather than fails the reconcile: a guild left without a zone reads as UTC and is
// filled on the next boot.
func fillZones(ctx context.Context, set *settings.Store, locales map[snowflake.ID]discord.Locale) int {
	n, err := set.FillZones(ctx, locales)
	if err != nil {
		slog.Error("settings: filling zones", slog.Any("err", err))
	}
	return n
}

// waitForGuilds blocks until every shard has reported and the GUILD_CREATE burst they announced has
// drained, and returns the guilds that never arrived. Those are unavailable, not gone, so they come
// back as a keep-list rather than as a failure: an error means only that a shard never spoke, which
// is the one case where the cache cannot be compared against anything.
func waitForGuilds(ctx context.Context, shards int, readied func() int, unready func() []snowflake.ID) ([]snowflake.ID, error) {
	if shards == 0 {
		return nil, fmt.Errorf("no shards to wait for")
	}
	ctx, cancel := context.WithTimeout(ctx, cacheSettle)
	defer cancel()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		// Order matters: an empty unready set is only meaningful once the last READY has seeded it.
		if readied() >= shards && len(unready()) == 0 {
			return nil, nil
		}
		select {
		case <-ctx.Done():
			if n := readied(); n < shards {
				return nil, fmt.Errorf("%d of %d shards without READY after %s: %w", n, shards, cacheSettle, ctx.Err())
			}
			return unready(), nil
		case <-tick.C:
		}
	}
}

func shardCount(c *bot.Client) int {
	if !c.HasShardManager() {
		return 0
	}
	var n int
	for range c.ShardManager.Shards() {
		n++
	}
	return n
}
