// Package bot is the Discord client itself: how it is configured, what it caches, and the handful
// of gateway listeners that are thin enough to belong nowhere else.
//
// It owns no application state beyond the join loop's own schedule. Commands live in
// internal/commands and channel selection in internal/voice — each taking what it needs from the
// client this package hands back.
package bot

import (
	"context"
	"fmt"
	"math/rand/v2"
	"slices"
	"time"

	"github.com/disgoorg/disgo"
	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/cache"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/gateway"
	"github.com/disgoorg/disgo/sharding"
	dvoice "github.com/disgoorg/disgo/voice"
	"github.com/disgoorg/snowflake/v2"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/be-sandaa/coucou/internal/bus"
	ev "github.com/be-sandaa/coucou/internal/events"
	"github.com/be-sandaa/coucou/internal/metrics"
	"github.com/be-sandaa/coucou/internal/silence"
	"github.com/be-sandaa/coucou/internal/sounds"
	"github.com/be-sandaa/coucou/internal/tracing"
	"github.com/be-sandaa/coucou/internal/voice"
)

// New builds the Discord client: intents, the deliberately tiny cache, and pure-Go DAVE. No
// listeners are registered here — the caller adds them, so that each one can declare its own
// dependencies instead of this function having to know them all.
//
// The member cache policy reads the client it is being built for, which is why the variable is
// declared before disgo.New and assigned after: a closure can be handed over before the thing it
// will read exists, and the policy never runs until it does.
// shardCount of 0 leaves the count to Discord, which is what a deploy should normally do — disgo
// asks GetGatewayBot for it and opens that many. Anything higher overrides it, and is the only way
// to exercise more than one shard before the bot is big enough to be given them.
func New(token string, shardCount int) (*bot.Client, error) {
	var c *bot.Client

	// A shard manager at every scale, rather than a lone gateway below some size and a manager
	// above it. At one shard the two behave identically, and the single path means nothing
	// downstream has to ask which it got — Client.Shard routes voice through either.
	shards := []sharding.ConfigOpt{
		sharding.WithGatewayConfigOpts(
			gateway.WithIntents(gateway.IntentGuilds, gateway.IntentGuildVoiceStates),
			gateway.WithLargeThreshold(50),                   // smallest GUILD_CREATE Discord allows
			gateway.WithCompression(gateway.CompressionNone), // no zlib inflate context per shard
			gateway.WithAutoReconnect(true),
		),
	}
	if shardCount > 0 {
		ids := make([]int, shardCount)
		for i := range ids {
			ids[i] = i
		}
		shards = append(shards, sharding.WithShardCount(shardCount), sharding.WithShardIDs(ids...))
	}

	client, err := disgo.New(token,
		// This makes disgo call GetGatewayBot while constructing, so New now does network I/O:
		// a bad token fails here rather than at open.
		bot.WithShardManagerConfigOpts(shards...),
		// The whole memory story: guilds, roles, voice states — and only the channels/members we read.
		bot.WithCacheConfigOpts(
			cache.WithCaches(cache.FlagGuilds|cache.FlagRoles|cache.FlagChannels|cache.FlagVoiceStates|cache.FlagMembers),
			cache.WithChannelCachePolicy(func(ch discord.GuildChannel) bool {
				// Stage channels are never usable (see voice.Usable) but stay cached, so /play from
				// inside one can name that as the reason rather than blaming permissions.
				return ch.Type() == discord.ChannelTypeGuildVoice || ch.Type() == discord.ChannelTypeGuildStageVoice
			}),
			cache.WithMemberCachePolicy(func(m discord.Member) bool {
				// ourselves (for permission checks) + anyone in voice (for the bot flag). Everyone else: no.
				if m.User.ID == c.ApplicationID {
					return true
				}
				_, inVoice := c.Caches.VoiceState(m.GuildID, m.User.ID)
				return inVoice
			}),
		),
		bot.WithVoiceManagerConfigOpts(dvoice.WithDaveSessionCreateFunc(voice.SessionCreateFunc)), // pure-Go DAVE
	)
	if err != nil {
		return nil, fmt.Errorf("discord client: %w", err)
	}
	c = client
	return client, nil
}

// VoiceMembers re-caches the members a GUILD_CREATE carried. disgo adds them before its voice
// states, so the in-voice policy drops them all, and voice.Humans then counts idle bots as people.
func VoiceMembers() []bot.EventListener {
	return []bot.EventListener{
		bot.NewListenerFunc(func(e *events.GuildReady) { recacheMembers(e.Client().Caches, e.Guild) }),
		bot.NewListenerFunc(func(e *events.GuildAvailable) { recacheMembers(e.Client().Caches, e.Guild) }),
		bot.NewListenerFunc(func(e *events.GuildJoin) { recacheMembers(e.Client().Caches, e.Guild) }),
	}
}

func recacheMembers(c cache.Caches, g discord.GatewayGuild) {
	for _, m := range g.Members {
		m.GuildID = g.ID
		c.AddMember(m)
	}
}

// Ready reports whether every shard has sent READY. It is the readiness signal and not the liveness
// one: the process can be perfectly healthy while a gateway reconnects, and restarting it then
// would only make the outage longer.
//
// Every shard, not a majority: the join loop walks every guild, so a shard that is down means that
// slice of servers silently gets no visits. Reporting ready while that is true would hide a partial
// outage from the only thing watching.
func Ready(c *bot.Client) bool {
	if !c.HasShardManager() {
		return false
	}
	var n int
	for shard := range c.ShardManager.Shards() {
		if shard.Status() != gateway.StatusReady {
			return false
		}
		n++
	}
	return n > 0 // no shards at all is not ready, it is not started
}

// NewPlayer returns the one way into a voice channel: join, wait, play, leave, publish the outcome.
//
// It is bounded rather than plain, and that is the point of it being one function. A play runs for
// up to playTimeout, the loop can ask for a hundred in a tick, and the cap is on simultaneous voice
// connections across the whole bot — so it has to sit here, below every caller, rather than in each.
// A full pool makes the caller wait, which is the backpressure; /play hands off to a goroutine at
// the composition root so a gateway handler never does that waiting.
//
// bus.Bounded is reused rather than reimplemented: its semaphore, its detach and its registration
// with Bus.Close's drain are exactly what a play needs, and none of that was ever about delivery.
// b is also where PlayFinished goes, which is the only part of a play that is still an event. set and
// quiet and opt are what an encore checks again before it comes back.
func NewPlayer(c *bot.Client, reg *sounds.Registry, quiet, opt *silence.Store, b *bus.Bus) func(context.Context, *PlayRequest) error {
	const (
		concurrency = 8
		playTimeout = 90 * time.Second
	)

	var player func(context.Context, *PlayRequest) error
	welcome := welcomer(
		quiet.Has,
		voice.Busy,
		func(g, ch snowflake.ID) []snowflake.ID { return voice.Humans(c, g, ch) },
		opt.Has,
	)

	one := func(ctx context.Context, v *visit) {
		e := v.req
		// The gauge is inside the detach, so it counts plays actually running rather than requests
		// accepted — the two differ by exactly the time a request waits for a slot.
		ctx, span := tracing.Tracer().Start(ctx, "play", trace.WithAttributes(
			attribute.String("coucou.guild", e.Guild.String()),
			attribute.String("coucou.channel", e.Channel.String()),
			attribute.String("coucou.trigger", e.Trigger),
		))
		defer span.End()

		metrics.VoiceActive.Inc()
		defer metrics.VoiceActive.Dec()

		nsfw := voice.AgeRestricted(c, e.Guild, e.Channel)
		sound := e.Sound
		if sound == "" {
			var ok bool
			if sound, ok = reg.Pick(nsfw); !ok {
				return
			}
		}
		file, ok := reg.Path(sound)
		if !ok {
			return // raced a deletion; nothing happened
		}
		if err := play(ctx, c, b, e, sound, file); !encoreDue(e, err) {
			return
		}
		if next, ok := reg.PickOther(sound, nsfw); ok {
			v.again(&PlayRequest{Guild: e.Guild, Channel: e.Channel, Sound: next, Trigger: string(ev.TriggerEncore)})
		}
	}
	pool := bus.Bounded(b, concurrency, bus.Timeout(playTimeout, one))
	player = func(ctx context.Context, e *PlayRequest) error {
		// Bound to the caller's ctx, not the body's: Bounded strips the cancellation from that one,
		// and an encore's wait still has to end on shutdown.
		again := func(next *PlayRequest) {
			go encore(ctx, time.Duration(10+rand.IntN(21))*time.Second, next, welcome, player)
		}
		return pool(ctx, &visit{req: e, again: again})
	}
	return player
}

// welcomer is the loop's checks, asked again for an encore. Opted-out people are subtracted the way
// voice.Best does it: a room holding only them is nobody worth coming back to.
func welcomer(
	quiet, busy func(guild snowflake.ID) bool,
	humans func(guild, channel snowflake.ID) []snowflake.ID,
	optedOut func(user snowflake.ID) bool,
) func(*PlayRequest) bool {
	return func(e *PlayRequest) bool {
		return !quiet(e.Guild) && !busy(e.Guild) &&
			slices.ContainsFunc(humans(e.Guild, e.Channel), func(u snowflake.ID) bool { return !optedOut(u) })
	}
}

// visit is what the pool runs: the request, and how to schedule the encore that may follow it.
type visit struct {
	req   *PlayRequest
	again func(next *PlayRequest)
}
