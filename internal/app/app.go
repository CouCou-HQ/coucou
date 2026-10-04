// Package app is the composition root: it turns arguments into a configuration, builds everything
// the bot needs in dependency order, and runs it until a signal or the first failure.
package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"time"

	dbot "github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/snowflake/v2"

	"github.com/be-sandaa/coucou/internal/bot"
	"github.com/be-sandaa/coucou/internal/bus"
	"github.com/be-sandaa/coucou/internal/bus/handlers"
	"github.com/be-sandaa/coucou/internal/chaos"
	"github.com/be-sandaa/coucou/internal/commands"
	ev "github.com/be-sandaa/coucou/internal/events"
	"github.com/be-sandaa/coucou/internal/logging"
	"github.com/be-sandaa/coucou/internal/metrics"
	"github.com/be-sandaa/coucou/internal/ops"
	"github.com/be-sandaa/coucou/internal/profile"
	"github.com/be-sandaa/coucou/internal/ranks"
	"github.com/be-sandaa/coucou/internal/rollup"
	"github.com/be-sandaa/coucou/internal/settings"
	"github.com/be-sandaa/coucou/internal/silence"
	"github.com/be-sandaa/coucou/internal/sounds"
	"github.com/be-sandaa/coucou/internal/store"
	"github.com/be-sandaa/coucou/internal/tracing"
	"github.com/be-sandaa/coucou/internal/voice"
	"github.com/be-sandaa/coucou/pkg/run"
)

// ErrUsage marks a bad invocation rather than a failure at runtime, so the caller can exit 2 the
// way a command is expected to for a usage error.
var ErrUsage = errors.New("usage")

// maxAckAge is four missed 41s heartbeats: a zombie shard reconnects well inside it, a wedged
// dispatch never does. See bot.Pulse.
const maxAckAge = 3 * time.Minute

// Run is the whole program. It returns an error instead of exiting so every deferred close still
// runs — the database in particular, which the final stats flush writes to.
//
// build is stamped into the binary at link time; it reaches the banner and the trace resource.
func Run(args []string, build Build) error {
	// A bootstrap logger at Info, because -log-level has not been read yet and a configuration
	// error still has to be printable.
	slog.SetDefault(slog.New(logging.New(os.Stdout, slog.LevelInfo)))

	cfg, err := parseConfig(args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return err // -h already printed the usage
		}
		return fmt.Errorf("%w: %w", ErrUsage, err)
	}
	// Now that -log-level has been read, replace the bootstrap logger. This is the last time it is
	// set: audit lines are ordinary Info records, so nothing downstream needs its own handler.
	slog.SetDefault(slog.New(logging.New(os.Stdout, cfg.LogLevel)))

	// The character is read before anything opens: a bot with a broken profile is misconfigured,
	// not degraded, and should say so before it touches the database or Discord.
	prof, err := profile.Load(cfg.ProfileDir)
	if err != nil {
		return fmt.Errorf("%w: profile: %w", ErrUsage, err)
	}

	return serve(cfg, prof, build)
}

// serve is the long-running path: open the database, build everything on top of it, and block
// until a signal or the first failure. The two modes that exit early — migrating and registering
// slash commands — return from inside it, because each needs part of what it builds.
func serve(cfg config, prof profile.Profile, build Build) error {
	r := run.New()
	defer r.Stop()

	// Database first, always. A failed migration fails the process before we ever touch Discord.
	// The backend is chosen by the URL scheme; both then wait for readiness and migrate the same way.
	db, err := store.Open(r, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("db: open: %w", err)
	}
	defer db.Close()
	if err := store.WaitAndMigrate(r, db); err != nil {
		return fmt.Errorf("db: refusing to start: %w", err)
	}

	p, err := assemble(r, cfg, prof, db)
	if err != nil {
		return err
	}
	// Commands are registered on the way up rather than by a separate one-off run, so a build can
	// never be serving a command set it does not have the handlers for. It is a REST call; the
	// gateway is not open yet.
	if err := commands.Deploy(p.client); err != nil {
		return err
	}

	ops.Serve(r, cfg.HTTPAddr, cfg.PProf, func() error {
		if age := p.pulse.Age(); age > maxAckAge {
			return fmt.Errorf("no gateway heartbeat ACK for %s", age.Round(time.Second))
		}
		return nil
	}, func(ctx context.Context) error {
		if !bot.Ready(p.client) {
			return errors.New("discord gateway is not ready")
		}
		return db.Ping(ctx)
	})

	// Registered after the bus and before the bot, so the exporter is shut down — and its batch
	// flushed — only once nothing is still producing spans.
	stopTracing, err := tracing.Init(r, cfg.OTLPEndpoint, build.Version)
	if err != nil {
		return fmt.Errorf("tracing: %w", err)
	}
	r.Add(nil, stopTracing)

	// Registration order is shutdown order reversed: the stats flusher is registered first so it is
	// stopped last and still has a live database to write its final batch to.
	r.Add(run.Ctx(p.events.Run), run.Ctx(p.events.Flush))
	r.Add(p.bus.Run, p.bus.Close)
	// Open, then reconcile, in that order and in one func. OpenShardManager returns once every
	// shard has had its READY, which makes the line after it the first moment the cache holds a
	// complete picture of what the bot is in — and a complete picture is exactly what the
	// reconcile compares the database against.
	r.Add(func(ctx context.Context) error {
		if err := p.client.OpenShardManager(ctx); err != nil {
			return err
		}
		if err := bot.SyncGuilds(ctx, p.client, p.ready, p.bus, db, p.settings, prof.Defaults); err != nil {
			return err
		}
		printBanner(os.Stdout, build, p.client, p.sounds)
		return nil
	}, func(ctx context.Context) error {
		// Plays first: Close would otherwise yank their voice conns mid-frame, under their own close.
		voice.Shutdown(ctx)
		p.client.Close(ctx)
		return nil
	})
	// Recurring opt-outs, quiet and chaos are windows the loop reads from memory; without these tickers
	// they would stay frozen at whichever occurrence was live when the process started.
	r.Add(p.optouts.Run, nil)
	r.Add(p.quiet.Run, nil)
	r.Add(p.chaos.Run, nil)
	r.Add(p.ranks.Run, nil)
	r.Add(p.commands.RunEmojis, nil)
	r.Add(p.rollup.Run, nil)
	r.Add(p.loop.Run, nil)

	slog.Info("running")
	return r.Run()
}

// assemble builds everything between the database and Discord — settings, the sound registry, the
// stats log, the event bus — then the bot that publishes to it and the consumers that react.
// parts is what serve registers, named rather than returned positionally — five values in a row
// is a signature nobody reads correctly twice.
type parts struct {
	client   *dbot.Client
	ready    *bot.ReadyTracker
	pulse    *bot.Pulse
	bus      *bus.Bus
	events   *ev.Log
	loop     *bot.Loop
	optouts  *silence.Store
	quiet    *silence.Store
	chaos    *chaos.Store
	ranks    *ranks.Cuts
	rollup   *rollup.Refresher
	settings *settings.Store
	sounds   *sounds.Registry
	commands *commands.Commands
}

// mem is the tables the bot answers from memory.
type mem struct {
	settings *settings.Store
	optouts  *silence.Store
	quiet    *silence.Store
	chaos    *chaos.Store
}

func mirrors(ctx context.Context, db store.Store) (mem, error) {
	m := mem{settings: settings.New(db), optouts: silence.OptOuts(db), quiet: silence.Quiet(db), chaos: chaos.New(db)}
	for name, load := range map[string]func(context.Context) error{
		"settings": m.settings.Load, "optouts": m.optouts.Load, "quiet": m.quiet.Load, "chaos": m.chaos.Load,
	} {
		if err := load(ctx); err != nil {
			return mem{}, fmt.Errorf("%s: load: %w", name, err)
		}
	}
	return m, nil
}

func assemble(r *run.Runner, cfg config, prof profile.Profile, db store.Store) (*parts, error) {
	m, err := mirrors(r, db)
	if err != nil {
		return nil, err
	}
	set, opt, quiet, cha := m.settings, m.optouts, m.quiet, m.chaos

	reg := sounds.New(prof.SoundsDir())
	reg.Arrange(prof.Chains, prof.Links)
	log := ev.New(db)
	eb, err := bus.New()
	if err != nil {
		return nil, fmt.Errorf("bus: %w", err)
	}
	// Set before Start so the registry never reads this field concurrently with a write.
	reg.OnChange = soundPublisher(r, eb)
	if err := reg.Start(r, cfg.SoundsPoll); err != nil {
		return nil, fmt.Errorf("sounds: start: %w", err)
	}

	client, err := bot.New(cfg.Token, cfg.ShardCount, prof.Status)
	if err != nil {
		return nil, err
	}
	// The player is built before the two things that use it — the loop and /play — because it is
	// the shared cap on simultaneous voice connections, not a per-caller one.
	player := bot.NewPlayer(client, reg, set, quiet, opt, eb)
	// /play hands off rather than calling straight through: a full pool makes the caller wait, and
	// the caller here is a gateway handler.
	//
	// WithoutCancel, not Background: the command's span is the trace root a play hangs off, and
	// keeping it is what the bus used to buy by propagating trace context through message metadata.
	// The cancellation has to go — the interaction's context dies when the handler returns, and a
	// play outlives it by ninety seconds or more.
	play := func(ctx context.Context, guild, channel snowflake.ID, sound string, user snowflake.ID) {
		ctx = context.WithoutCancel(ctx)
		go func() {
			if err := player(ctx, &bot.PlayRequest{
				Guild: guild, Channel: channel, Sound: sound, Trigger: string(ev.TriggerCommand), User: &user,
			}); err != nil {
				slog.Debug("play: refused", slog.Any("err", err))
			}
		}()
	}
	rk := ranks.New(db)
	cmds := commands.New(client, set, opt, quiet, cha, reg, log, rk, eb, play, prof, cfg.OwnerIDs, cfg.Siblings)
	ready := bot.NewReadyTracker()
	pulse := bot.NewPulse()

	// Everything coming in from Discord. Each listener is built with what it acts on and nothing
	// more, which is why two of them take the client and the rest do not.
	// The guild reconcile is not here. It needs every shard to have reported, which no gateway
	// event says — serve sequences it after OpenShardManager instead. See bot.SyncGuilds.
	// The gateway is a watermill subscriber: its listeners push Discord's own payloads onto
	// discord.* topics, and handlers consume them through the same router and middleware as
	// everything else. Interactions are the exception and stay synchronous — three seconds is not
	// enough for a delivery that retries.
	gw := bot.NewGateway()
	client.AddEventListeners(slices.Concat(gw.Listeners(), bot.VoiceMembers(), cmds.Listeners(),
		[]dbot.EventListener{ready.OnReady(), pulse.OnHeartbeatAck()})...)

	// And everything going the other way. The two blocks together are every edge of the system:
	// gateway in above, bus out below.
	//
	// The fan-out map of the whole bot. It lives here, not inside the handlers package, so each one
	// declares exactly what it acts on; the cost is that a new handler has to be added here too and
	// nothing but review will notice if it is not.
	//
	// These names label the bus metrics and spans. Don't rename them casually.
	bus.On(eb, "stats-plays", handlers.PlayFinished(log))
	bus.On(eb, "stats-commands", handlers.CommandInvoked(log))
	bus.OnTopic(eb, gw, "stats-guilds-join", bot.TopicGuildCreate, handlers.GuildJoinedStats(log))
	bus.OnTopic(eb, gw, "stats-guilds-leave", bot.TopicGuildDelete, handlers.GuildLeftStats(log))
	bus.On(eb, "stats-guilds-reconciled", handlers.GuildReconciledStats(log))

	bus.OnTopic(eb, gw, "guild-sync-join", bot.TopicGuildCreate, handlers.GuildJoinedSync(db, set, prof.Defaults))
	bus.OnTopic(eb, gw, "guild-sync-leave", bot.TopicGuildDelete, handlers.GuildLeftSync(db))

	bus.On(eb, "log-sounds-added", handlers.SoundAdded(log))
	bus.On(eb, "log-sounds-removed", handlers.SoundRemoved(log))
	bus.On(eb, "log-settings", handlers.SettingsChanged(log))

	metrics.ObserveGuilds(func() int { return len(set.Configured()) })
	metrics.ObserveSounds(reg.Len)
	metrics.ObserveBuffered(log.Buffered)
	metrics.ObserveGatewayLatency(bot.GatewayLatency(client))

	return &parts{
		client:   client,
		ready:    ready,
		pulse:    pulse,
		bus:      eb,
		events:   log,
		loop:     bot.NewLoop(client, set, reg, player, opt, quiet, cha),
		optouts:  opt,
		quiet:    quiet,
		chaos:    cha,
		ranks:    rk,
		rollup:   rollup.New(db),
		settings: set,
		sounds:   reg,
		commands: cmds,
	}, nil
}

// soundPublisher turns registry changes into bus events. Nothing may publish before the router has
// attached its subscribers, so until then a change is only logged — the registry itself is already
// up to date, and these events have no consumer but the log.
func soundPublisher(ctx context.Context, eb *bus.Bus) func(name string, added bool) {
	return func(name string, added bool) {
		select {
		case <-eb.Running():
		default:
			slog.Info("sound changed before the bus was up", slog.String("name", name), slog.Bool("added", added))
			return
		}
		if added {
			eb.Publish(ctx, bus.SoundAdded{Name: name})
		} else {
			eb.Publish(ctx, bus.SoundRemoved{Name: name})
		}
	}
}
