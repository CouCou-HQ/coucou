// Package handlers holds every consumer on the event bus. Nothing here talks to Discord: the bot
// publishes what happened, this package decides what that causes.
//
// Playing is deliberately not here. A play is something the bot does on request, not something a
// delivery causes — and no ack can honestly cover the 90 seconds one can take. The loop and /play
// call bot.NewPlayer directly; what reaches this package is PlayFinished, the fact that it ended.
//
//	discord.guild_create ─► guild sync (db) ─► stats writer
//	discord.guild_delete ─► guild sync (db) ─► stats writer
//	PlayFinished   ─► stats writer
//	CommandInvoked ─► stats writer
//	GuildLeft      ─► stats writer (boot reconcile only)
//	SoundAdded/Removed             ─► (nobody yet; log only)
//	SettingsChanged                ─► console line only (the record is the settings trigger, or the quiet row)
//
// Each handler is built by its own constructor taking exactly what it acts on, and the result goes
// to bus.On at the composition root. Names match the event they consume; the two events that have
// two consumers each get the consumer's job as a suffix.
package handlers

import (
	"context"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"

	"github.com/be-sandaa/coucou/internal/bus"
	"github.com/be-sandaa/coucou/internal/events"
	"github.com/be-sandaa/coucou/internal/metrics"
	"github.com/be-sandaa/coucou/internal/settings"
	"github.com/be-sandaa/coucou/internal/store"
)

// PlayFinished records the outcome of a play. This one keeps a direct handle on the log rather
// than going through an audit record: a play is a relational row with its listeners in a second
// table, which is more than a log record can carry.
func PlayFinished(log *events.Log) bus.Handler[bus.PlayFinished] {
	return func(_ context.Context, e *bus.PlayFinished) error {
		outcome := e.Reason
		if e.OK {
			outcome = "ok"
		}
		metrics.PlaysTotal.WithLabelValues(e.Trigger, outcome).Inc()
		metrics.PlayDuration.WithLabelValues(e.Trigger).Observe(e.Duration.Seconds())
		log.RecordPlay(events.Play{
			At: e.StartedAt, Guild: e.Guild, Channel: e.Channel, Sound: e.Sound, Trigger: e.Trigger,
			User: e.User, ListenerIDs: e.Listeners, FledIDs: e.Fled, OK: e.OK, Reason: e.Reason, Duration: e.Duration,
		})
		return nil
	}
}

// keyName is a stored column name, shared by the three records that carry one. The data keys are
// schema — renaming this is a migration, not a wording change.
const keyName = "name"

// The guild_leave record and its one column, shared by the live handler and the boot-reconcile one
// so the two cannot drift into writing different rows for the same thing.
const (
	kindGuildLeave = "guild_leave"
	keyReconciled  = "reconciled"
)

// The three below are audit records: Log.Audit both stores the row and prints the line. Kind is the
// row kind and the data keys are its columns, so both are schema — renaming one is a migration, not
// a wording change.

func CommandInvoked(log *events.Log) bus.Handler[bus.CommandInvoked] {
	return func(ctx context.Context, e *bus.CommandInvoked) error {
		// The only record carrying its own time: the command ran when the user pressed enter, which
		// may be a retry and a backlog ago.
		log.Audit(ctx, events.Misc{At: e.At, Kind: "command", Guild: &e.Guild, User: &e.User,
			Data: map[string]any{keyName: e.Name}})
		return nil
	}
}

func GuildJoinedStats(log *events.Log) bus.Handler[discord.GatewayGuild] {
	return func(ctx context.Context, g *discord.GatewayGuild) error {
		log.Audit(ctx, events.Misc{Kind: "guild_join", Guild: &g.ID,
			Data: map[string]any{"memberCount": g.MemberCount}})
		return nil
	}
}

func GuildLeftStats(log *events.Log) bus.Handler[discord.Guild] {
	return func(ctx context.Context, g *discord.Guild) error {
		log.Audit(ctx, events.Misc{Kind: kindGuildLeave, Guild: &g.ID,
			Data: map[string]any{keyReconciled: false}})
		return nil
	}
}

// GuildReconciledStats is the same record for the guilds found missing at boot. It stays a domain
// event because it is one: nothing left on the gateway, the bot was simply not running when it
// happened, and Discord has no frame for that.
func GuildReconciledStats(log *events.Log) bus.Handler[bus.GuildLeft] {
	return func(ctx context.Context, e *bus.GuildLeft) error {
		log.Audit(ctx, events.Misc{Kind: kindGuildLeave, Guild: &e.Guild,
			Data: map[string]any{keyReconciled: true}})
		return nil
	}
}

// GuildJoinedSync keeps the guilds table and the settings seed in step with Discord, and fills in a
// zone guessed from the guild's locale if it has none.
// d seeds a guild that has no settings row yet; a guild overrides each value with its command, and
// seeding never overwrites a row that already exists.
func GuildJoinedSync(db store.Store, set *settings.Store, d store.Defaults) bus.Handler[discord.GatewayGuild] {
	return func(ctx context.Context, g *discord.GatewayGuild) error {
		if err := db.UpsertGuilds(ctx, []store.Guild{{ID: g.ID, JoinedAt: g.JoinedAt}}); err != nil {
			return err // retried by middleware, then dropped with a log line
		}
		if err := db.SeedSettingsFor(ctx, g.ID, d); err != nil {
			return err
		}
		// A guild coming back already has its row, and the mirror with it: writing the defaults over
		// it here would undo the seed's do-not-overwrite rule.
		if d != (store.Defaults{}) && !set.Known(g.ID) {
			// Actor zero: nobody asked for this, the bot is applying its own defaults.
			if _, err := set.Update(ctx, g.ID, 0, func(s *settings.Settings) {
				s.Chance, s.Suspense, s.FakeOut, s.Encore = d.Chance, d.Suspense, d.FakeOut, d.Encore
			}); err != nil {
				return err
			}
		}
		// The join payload carries the locale, so the zone is filled in now rather than at the next
		// boot. A guild coming back keeps the zone it had.
		_, err := set.FillZones(ctx, map[snowflake.ID]discord.Locale{g.ID: discord.Locale(g.PreferredLocale)})
		return err
	}
}

// GuildLeftSync has no reconciled branch any more: a boot reconcile writes its own rows and
// publishes bus.GuildLeft, which this does not consume. What reaches here left while we watched.
func GuildLeftSync(db store.Store) bus.Handler[discord.Guild] {
	return func(ctx context.Context, g *discord.Guild) error {
		return db.MarkGuildLeft(ctx, g.ID)
	}
}

// The three below are audit records with nothing to act on. A sound belongs to no guild, which is
// why those two leave Guild nil — the column is null for them.

func SoundAdded(log *events.Log) bus.Handler[bus.SoundAdded] {
	return func(ctx context.Context, e *bus.SoundAdded) error {
		log.Audit(ctx, events.Misc{Kind: "sound_added", Data: map[string]any{keyName: e.Name}})
		return nil
	}
}

func SoundRemoved(log *events.Log) bus.Handler[bus.SoundRemoved] {
	return func(ctx context.Context, e *bus.SoundRemoved) error {
		log.Audit(ctx, events.Misc{Kind: "sound_removed", Data: map[string]any{keyName: e.Name}})
		return nil
	}
}

// SettingsChanged only prints. The record itself is written by the audit trigger on guild_settings,
// which sees the old and new values this event does not carry — it knows which command ran, not what
// the setting became. The line stays because a console following the bot should still show it.
func SettingsChanged(log *events.Log) bus.Handler[bus.SettingsChanged] {
	return func(ctx context.Context, e *bus.SettingsChanged) error {
		log.Print(ctx, events.Misc{Kind: "settings_changed", Guild: &e.Guild, User: &e.By,
			Data: map[string]any{"field": e.Field}})
		return nil
	}
}
