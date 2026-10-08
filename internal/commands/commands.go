// Package commands is the slash-command half of the Discord edge: the definitions, the dispatcher
// that routes an interaction to its handler, and the handlers themselves.
//
// These cannot be bus consumers however much they resemble one. An interaction has to be answered
// within three seconds, so a command replies synchronously through the cache and the REST API
// rather than publishing a fact and returning — which is why this package takes six dependencies
// where a bus handler takes one.
package commands

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"maps"
	"math/rand/v2"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/omit"
	"github.com/disgoorg/snowflake/v2"
	"github.com/teambition/rrule-go"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/sync/errgroup"

	"github.com/be-sandaa/coucou/internal/bus"
	"github.com/be-sandaa/coucou/internal/chaos"
	"github.com/be-sandaa/coucou/internal/characters"
	ev "github.com/be-sandaa/coucou/internal/events"
	"github.com/be-sandaa/coucou/internal/profile"
	"github.com/be-sandaa/coucou/internal/ranks"
	"github.com/be-sandaa/coucou/internal/schedule"
	"github.com/be-sandaa/coucou/internal/settings"
	"github.com/be-sandaa/coucou/internal/silence"
	"github.com/be-sandaa/coucou/internal/sounds"
	"github.com/be-sandaa/coucou/internal/store"
	"github.com/be-sandaa/coucou/internal/tracing"
	"github.com/be-sandaa/coucou/internal/voice"
)

// Play is how /play reaches a voice channel. A func rather than the player itself, so this package
// keeps knowing nothing about how a play is bounded or traced — and so the hand-off that stops a
// gateway handler waiting on a full pool lives at the composition root, where it is visible.
type Play func(ctx context.Context, a PlayArgs)

// PlayArgs is one play asked for by a command. Character and Preview are /character preview's: a
// character other than the guild's, and the sounds to play as it.
type PlayArgs struct {
	Guild, Channel, User snowflake.ID
	Sound                string
	Character            string
	Preview              []string
}

// Commands is the dispatcher. Build one and register its listeners on the client.
type Commands struct {
	client   *bot.Client
	settings *settings.Store
	optouts  *silence.Store
	quiet    *silence.Store
	chaos    *chaos.Store
	chars    *characters.Set
	events   *ev.Log
	ranks    *ranks.Cuts
	bus      *bus.Bus
	play     Play
	owners   []snowflake.ID
	siblings []Sibling
	note     string
	// push queues a guild to take on its character's nickname and avatar.
	push func(...snowflake.ID)
	// Zero value works, so New never has to mention it and a test can build a Commands literal.
	autocomplete debouncer
	plays        cooldown
	switches     guildCooldown
	emojis       emojis
}

// Sibling is another bot running on coucou that /help points people to: a different sound set run by the same
// people. The binary does not know which bot it is, so the list comes from config and includes this
// one; /help leaves out whichever entry is itself.
type Sibling struct {
	Name string
	App  snowflake.ID
}

// New takes this many arguments because a command reads settings, opt-outs, quiet and chaos, picks
// sounds, queries stats and ranks, publishes facts, plays something and answers through the cache and
// REST — all inside one three-second interaction. Every parameter but opt and quiet is a distinct
// type, so getting the order wrong is mostly a compile error rather than a bug.
func New(
	client *bot.Client,
	set *settings.Store,
	opt, quiet *silence.Store,
	ch *chaos.Store,
	chars *characters.Set,
	log *ev.Log,
	rk *ranks.Cuts,
	b *bus.Bus,
	play Play,
	owners []snowflake.ID,
	siblings []Sibling,
	note string,
	push func(...snowflake.ID),
) *Commands {
	colBrand = chars.Default().Color
	return &Commands{client: client, settings: set, optouts: opt, quiet: quiet, chaos: ch, chars: chars, events: log, ranks: rk, bus: b, play: play, owners: owners, siblings: siblings, note: note, push: push}
}

// character is who the bot is in guild.
func (c *Commands) character(guild snowflake.ID) *characters.Character {
	return c.chars.Get(c.settings.Get(guild).Character)
}

// isOwner gates the servers leaderboard. A linear scan over a handful of ids is cheaper than the map
// that would replace it, and this runs once per command.
func (c *Commands) isOwner(user snowflake.ID) bool { return slices.Contains(c.owners, user) }

// OnCommand and OnAutocomplete are the two gateway listeners this package owns. They are built
// here rather than in internal/bot so the dispatcher's dependencies stay its own.
//
// Neither answers its interaction on the calling goroutine, and that is not a speed optimisation.
// disgo runs listeners inline on the shard's websocket read goroutine, under a mutex shared by
// every shard, and that goroutine is the only thing that reads HEARTBEAT_ACK — so answering over
// REST stops the heartbeat being acknowledged on every shard until Discord replies, and a throttled
// bucket makes that seconds.
//
// A command gets a goroutine, unbounded on purpose: a bound has to drop an interaction to mean
// anything, and a dropped command is "the application did not respond" in the user's face. Each
// already carries its own 10s deadline, and Discord throttles interactions long before the
// goroutines are worth counting. Autocomplete needs no goroutine here — the debounce timer is one.
// Listeners is every gateway listener this package owns: commands, autocomplete and buttons.
func (c *Commands) Listeners() []bot.EventListener {
	return []bot.EventListener{c.OnCommand(), c.OnAutocomplete(), c.OnComponent()}
}

func (c *Commands) OnCommand() bot.EventListener {
	return bot.NewListenerFunc(func(e *events.ApplicationCommandInteractionCreate) { go c.onCommand(e) })
}

func (c *Commands) OnAutocomplete() bot.EventListener {
	return bot.NewListenerFunc(c.onAutocomplete)
}

var manageGuild = discord.PermissionManageGuild

// descWhich describes an option that picks one of a list.
const descWhich = "Which one"

// Command names, shared by the definitions and the dispatcher.
const (
	cmdNamePlay        = "play"
	cmdNameSounds      = "sounds"
	cmdNameLeave       = "leave"
	cmdNameChance      = "chance"
	cmdNameQuiet       = "quiet"
	cmdNameChaos       = "chaos"
	cmdNameSuspense    = "suspense"
	cmdNameStatus      = "status"
	cmdNameStats       = "stats"
	cmdNameLeaderboard = "leaderboard"
	cmdNameOptOut      = "optout"
	cmdNameHelp        = "help"
	cmdNameAbout       = "about"
	cmdNameInvite      = "invite"
)

// Leaderboard board values, shared by the choice list and the renderer.
const (
	boardHeard     = "heard"
	boardTriggered = "triggered"
	boardFled      = "fled"
	boardSounds    = "sounds"
	boardChannels  = "channels"
	boardGuilds    = "guilds"
)

// Board titles, shared by the choice list shown in Discord and the leaderboard heading.
const (
	titleHeard     = "Most heard"
	titleTriggered = "Most triggered"
	titleFled      = "Rage quits"
	titleSounds    = "Top sounds"
	titleChannels  = "Most visited channels"
	titleGuilds    = "Top servers"
)

// boardTitles maps a board value to the heading its report is printed under.
var boardTitles = map[string]string{
	boardHeard:     titleHeard,
	boardTriggered: titleTriggered,
	boardFled:      titleFled,
	boardSounds:    titleSounds,
	boardChannels:  titleChannels,
	boardGuilds:    titleGuilds,
}

// /stats scopes: whose numbers the report is about.
const (
	scopeUser  = "user"
	scopeGuild = "guild"
	scopeBot   = "bot"
)

const (
	fieldScope  = "scope" // the /stats option, not the OAuth query key of the same name
	optOn       = "on"
	optFor      = "for"
	optSchedule = "schedule"
	optRule     = "rrule"
	optOff      = "off"
	optPage     = "page"
	periodAll   = "all"
	fieldFrom   = "from"
	fieldHours  = "hours"
	fieldTZ     = "tz"
	fieldDays   = "days"
	fieldRule   = "rule"
	fieldChance = "chance"
	subSet      = "set"
	descDays    = "Which days"
	descFrom    = "Hour it starts, 0-23"
	descHours   = "How long it lasts, 1-24"
	activeNow   = " · active now"
	tzHint      = "IANA zone; defaults to this server's"
)

// dayChoices doubles as the BYDAY value it produces, so choosing "Weekdays" and typing
// BYDAY=MO,TU,WE,TH,FR store the same rule.
var dayChoices = []discord.ApplicationCommandOptionChoiceString{
	{Name: "Every day", Value: "MO,TU,WE,TH,FR,SA,SU"},
	{Name: "Weekdays", Value: "MO,TU,WE,TH,FR"},
	{Name: "Weekends", Value: "SA,SU"},
	{Name: "Mondays", Value: "MO"},
	{Name: "Tuesdays", Value: "TU"},
	{Name: "Wednesdays", Value: "WE"},
	{Name: "Thursdays", Value: "TH"},
	{Name: "Fridays", Value: "FR"},
	{Name: "Saturdays", Value: "SA"},
	{Name: "Sundays", Value: "SU"},
}

// dayLabel is the reverse, for a reply that says "Weekdays" rather than "MO,TU,WE,TH,FR".
func dayLabel(value string) string {
	for _, ch := range dayChoices {
		if ch.Value == value {
			return ch.Name
		}
	}
	return value
}

// silenceSubs is the subcommands /optout and /quiet share: the same shapes, worded for whose it is.
func silenceSubs(on, timed, sched, off string) []discord.ApplicationCommandOption {
	return []discord.ApplicationCommandOption{
		discord.ApplicationCommandOptionSubCommand{Name: optOn, Description: on},
		// Whole hours, capped at a day. Anything longer is what on is for, and an integer option
		// needs no parsing and no way to be typed wrongly.
		discord.ApplicationCommandOptionSubCommand{Name: optFor, Description: timed, Options: []discord.ApplicationCommandOption{
			discord.ApplicationCommandOptionInt{Name: fieldHours, Description: "1-24", Required: true, MinValue: ptr(1), MaxValue: ptr(24)},
		}},
		// The guided form and the raw one are both rules; the picker exists because nobody types
		// BYDAY from memory, not because the two mean different things.
		discord.ApplicationCommandOptionSubCommand{Name: optSchedule, Description: sched, Options: []discord.ApplicationCommandOption{
			discord.ApplicationCommandOptionString{Name: fieldDays, Description: descDays, Required: true, Choices: dayChoices},
			discord.ApplicationCommandOptionInt{Name: fieldFrom, Description: descFrom, Required: true, MinValue: ptr(0), MaxValue: ptr(23)},
			discord.ApplicationCommandOptionInt{Name: fieldHours, Description: descHours, Required: true, MinValue: ptr(1), MaxValue: ptr(24)},
			discord.ApplicationCommandOptionString{Name: fieldTZ, Description: tzHint},
		}},
		discord.ApplicationCommandOptionSubCommand{Name: optRule, Description: "The same, as a raw RFC 5545 rule", Options: []discord.ApplicationCommandOption{
			discord.ApplicationCommandOptionString{Name: fieldRule, Description: "e.g. FREQ=WEEKLY;BYDAY=MO,WE;BYHOUR=20", Required: true},
			discord.ApplicationCommandOptionInt{Name: fieldHours, Description: "How long each one lasts, 1-24", Required: true, MinValue: ptr(1), MaxValue: ptr(24)},
			discord.ApplicationCommandOptionString{Name: fieldTZ, Description: tzHint},
		}},
		discord.ApplicationCommandOptionSubCommand{Name: optOff, Description: off},
	}
}

var definitions = []discord.ApplicationCommandCreate{
	discord.SlashCommandCreate{
		Name: cmdNamePlay, Description: "Play a sound in your voice channel",
		Options: []discord.ApplicationCommandOption{
			discord.ApplicationCommandOptionString{Name: "sound", Description: descWhich, Autocomplete: true},
		},
	},
	discord.SlashCommandCreate{
		Name: cmdNameSounds, Description: "Every sound you can ask /play for",
		Options: []discord.ApplicationCommandOption{
			discord.ApplicationCommandOptionInt{Name: optPage, Description: "Default: 1", MinValue: ptr(1)},
		},
	},
	discord.SlashCommandCreate{Name: cmdNameLeave, Description: "Make the bot leave voice"},
	discord.SlashCommandCreate{Name: cmdNameHelp, Description: "What the commands do, and how to keep the bot out"},
	discord.SlashCommandCreate{Name: cmdNameAbout, Description: "Who this bot is"},
	discord.SlashCommandCreate{Name: cmdNameInvite, Description: "Add the bot to another server"},
	discord.SlashCommandCreate{
		Name: cmdNameOptOut, Description: "Stop the bot counting you when it picks a channel",
		Options: silenceSubs("Do not count me, until I say otherwise", "Do not count me for a while",
			"Do not count me on a repeating schedule", "Count me again"),
	},
	forgetDefinition,
	discord.SlashCommandCreate{
		Name: cmdNameChance, Description: "How likely the bot drops in every 5 min (0 = never)",
		DefaultMemberPermissions: omit.NewPtr(manageGuild),
		Options: []discord.ApplicationCommandOption{
			discord.ApplicationCommandOptionInt{Name: "percent", Description: "0-100", Required: true, MinValue: ptr(0), MaxValue: ptr(100)},
			discord.ApplicationCommandOptionInt{Name: optEncore, Description: "% of visits that come back for a second sound (0 = off)", MinValue: ptr(0), MaxValue: ptr(maxEncore)},
		},
	},
	discord.SlashCommandCreate{
		Name: cmdNameQuiet, Description: "Times when the bot leaves this server alone",
		DefaultMemberPermissions: omit.NewPtr(manageGuild),
		Options: silenceSubs("Leave this server alone, until someone says otherwise", "Leave this server alone for a while",
			"Leave this server alone on a repeating schedule", "Stop being quiet"),
	},
	discord.SlashCommandCreate{
		Name: cmdNameChaos, Description: "A weekly window when the bot drops in more often",
		DefaultMemberPermissions: omit.NewPtr(manageGuild),
		Options: []discord.ApplicationCommandOption{
			discord.ApplicationCommandOptionSubCommand{Name: subSet, Description: "Set the window (local time)", Options: []discord.ApplicationCommandOption{
				discord.ApplicationCommandOptionString{Name: fieldDays, Description: descDays, Required: true, Choices: dayChoices},
				discord.ApplicationCommandOptionInt{Name: fieldFrom, Description: descFrom, Required: true, MinValue: ptr(0), MaxValue: ptr(23)},
				discord.ApplicationCommandOptionInt{Name: fieldHours, Description: descHours, Required: true, MinValue: ptr(1), MaxValue: ptr(24)},
				discord.ApplicationCommandOptionInt{Name: fieldChance, Description: "Percent inside the window, above /chance", Required: true, MinValue: ptr(1), MaxValue: ptr(100)},
				discord.ApplicationCommandOptionString{Name: fieldTZ, Description: tzHint},
			}},
			discord.ApplicationCommandOptionSubCommand{Name: optOff, Description: "Back to the usual odds"},
		},
	},
	discord.SlashCommandCreate{
		Name: cmdNameSuspense, Description: "Sit in silence before the sound. Unsettling.",
		DefaultMemberPermissions: omit.NewPtr(manageGuild),
		Options: []discord.ApplicationCommandOption{
			discord.ApplicationCommandOptionInt{Name: "seconds", Description: "Max seconds of silence (0 = off)", Required: true, MinValue: ptr(0), MaxValue: ptr(maxSuspense)},
			discord.ApplicationCommandOptionInt{Name: optFakeOut, Description: "% of visits that leave without a sound (0 = off)", MinValue: ptr(0), MaxValue: ptr(maxFakeOut)},
		},
	},
	discord.SlashCommandCreate{
		Name: cmdNameNSFW, Description: "Where 18+ sounds may play here; shows the setting without a mode",
		DefaultMemberPermissions: omit.NewPtr(manageGuild),
		Options: []discord.ApplicationCommandOption{
			discord.ApplicationCommandOptionString{Name: optMode, Description: "Leave out to see the current one", Choices: []discord.ApplicationCommandOptionChoiceString{
				{Name: "off: never", Value: string(settings.NSFWOff)},
				{Name: "restricted: only where Discord has age-restricted the server or channel", Value: string(settings.NSFWRestricted)},
				{Name: "on: every voice channel, asks to confirm first", Value: string(settings.NSFWOn)},
			}},
		},
	},
	discord.SlashCommandCreate{Name: cmdNameStatus, Description: "What the bot thinks about this server"},
	discord.SlashCommandCreate{
		Name: cmdNameStats, Description: "Play statistics: yours, this server's, or bot-wide",
		Options: []discord.ApplicationCommandOption{
			discord.ApplicationCommandOptionString{Name: fieldScope, Description: "Whose numbers", Required: true, Choices: []discord.ApplicationCommandOptionChoiceString{
				{Name: "User", Value: scopeUser},
				{Name: "Guild", Value: scopeGuild},
				{Name: "Bot", Value: scopeBot},
			}},
		},
	},
	discord.SlashCommandCreate{
		Name: cmdNameLeaderboard, Description: "Who suffers the most",
		Options: []discord.ApplicationCommandOption{
			discord.ApplicationCommandOptionString{Name: "board", Description: "Which board", Required: true, Choices: []discord.ApplicationCommandOptionChoiceString{
				{Name: "Most heard (present during plays)", Value: boardHeard},
				{Name: "Most triggered (/play)", Value: boardTriggered},
				{Name: "Rage quits (left before it ended)", Value: boardFled},
				{Name: titleSounds, Value: boardSounds},
				{Name: titleChannels, Value: boardChannels},
				{Name: "Top servers (owner)", Value: boardGuilds},
			}},
			discord.ApplicationCommandOptionString{Name: "period", Description: "Default: 30 days", Choices: []discord.ApplicationCommandOptionChoiceString{
				{Name: "7 days", Value: "7"}, {Name: "30 days", Value: "30"}, {Name: "All time", Value: periodAll},
			}},
			discord.ApplicationCommandOptionBool{Name: "public", Description: "Post it in the channel for everyone"},
		},
	},
}

func ptr[T any](v T) *T { return &v }

// maxSuspense is the option's ceiling and the meter's denominator. One constant, because a bar
// drawn against a different maximum than the one Discord enforces is a bar that lies.
const maxSuspense = profile.MaxSuspense

const (
	optFakeOut = "fakeout"
	maxFakeOut = profile.MaxFakeOut
	optEncore  = "encore"
	maxEncore  = profile.MaxEncore
)

// Embed colours. Four of them, and every title states in words what its colour states in hue — a
// screen reader is read the title and never the colour, so the colour is confirmation, not carrier.
//
// colBrand is the character's accent from its profile. A var set once in New rather than a field:
// one process runs one profile, and info is called from everywhere.
var colBrand = profile.DefaultColor // reports and confirmations

const (
	colBoard = 0xF2B705 // leaderboards, matching the medal
	colBad   = 0xC4413B // refusals, bad input, anything that failed
	colMuted = 0x4E5058 // nothing to show, which is not the same as failing
)

func embed(color int, title, body string) discord.Embed {
	return discord.NewEmbed().WithColor(color).WithTitle(title).WithDescription(body)
}

func info(title, body string) discord.Embed { return embed(colBrand, title, body) }
func bad(title, body string) discord.Embed  { return embed(colBad, title, body) }

// deny is bad for the callers that hand a refusal back up rather than sending it themselves.
func deny(title, body string) *discord.Embed { e := bad(title, body); return &e }

func none(title, body string) discord.Embed { return embed(colMuted, title, body) }

// say is the reply every command gives: its embeds, ephemeral. A public leaderboard is the only
// thing in here that reaches the channel, and it goes out through edit instead.
func say(es ...discord.Embed) discord.MessageCreate {
	return discord.NewMessageCreate().WithEmbeds(es...).WithEphemeral(true)
}

// plural renders a count with its noun, because "1 channels" is how a bot looks unfinished.
func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

// onCommand is the single interaction listener; routes by command name.
func (c *Commands) onCommand(e *events.ApplicationCommandInteractionCreate) {
	defer logPanic("command")
	if e.GuildID() == nil {
		if err := e.CreateMessage(say(bad("Servers only", "This one does nothing in a DM."))); err != nil {
			slog.Error("replying to a DM interaction", slog.Any("err", err))
		}
		return
	}
	guild := *e.GuildID()
	data := e.SlashCommandInteractionData()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// The root of the trace: everything a command causes, including the play it publishes, hangs
	// off this. A gateway event arrives with no upstream context, so there is nothing to continue.
	ctx, span := tracing.Tracer().Start(ctx, "command "+data.CommandName(), trace.WithAttributes(
		attribute.String("coucou.guild", guild.String()),
		attribute.String("coucou.command", data.CommandName()),
	))
	defer span.End()
	c.bus.Publish(ctx, bus.CommandInvoked{Guild: guild, User: e.User().ID, Name: data.CommandName(), At: time.Now()})

	handler, ok := c.handlers()[data.CommandName()]
	if !ok {
		slog.Warn("no handler for command", slog.String("name", data.CommandName()))
		return
	}
	if err := handler(ctx, e, guild, data); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		slog.Error("command failed", slog.String("name", data.CommandName()), slog.Any("err", err))
	}
}

// logPanic keeps a bug in one interaction from taking every shard down with it. Deferred at the top
// of each goroutine this package starts; a recover only catches panics on its own goroutine.
func logPanic(where string) {
	if r := recover(); r != nil {
		slog.Error("panic", slog.String("in", where), slog.Any("panic", r), slog.String("stack", string(debug.Stack())))
	}
}

// cmdFunc is the one shape every slash command handler takes, so the dispatcher can be a table.
type cmdFunc func(context.Context, *events.ApplicationCommandInteractionCreate, snowflake.ID, discord.SlashCommandInteractionData) error

func (c *Commands) handlers() map[string]cmdFunc {
	return map[string]cmdFunc{
		cmdNamePlay:        c.cmdPlay,
		cmdNameSounds:      c.cmdSounds,
		cmdNameLeave:       c.cmdLeave,
		cmdNameChance:      c.cmdChance,
		cmdNameQuiet:       c.cmdQuiet,
		cmdNameChaos:       c.cmdChaos,
		cmdNameSuspense:    c.cmdSuspense,
		cmdNameNSFW:        c.cmdNSFW,
		cmdNameStatus:      c.cmdStatus,
		cmdNameStats:       c.cmdStats,
		cmdNameLeaderboard: c.cmdLeaderboard,
		cmdNameOptOut:      c.cmdOptOut,
		cmdNameHelp:        c.cmdHelp,
		cmdNameAbout:       c.cmdAbout,
		cmdNameInvite:      c.cmdInvite,
		cmdNameCharacter:   c.cmdCharacter,
		cmdNameForget:      c.cmdForget,
	}
}

func (c *Commands) cmdLeave(_ context.Context, e *events.ApplicationCommandInteractionCreate, guild snowflake.ID, _ discord.SlashCommandInteractionData) error {
	if voice.Cancel(guild) {
		return e.CreateMessage(say(info("Gone", "")))
	}
	return e.CreateMessage(say(none("Wasn't here", "")))
}

func (c *Commands) cmdChance(ctx context.Context, e *events.ApplicationCommandInteractionCreate, guild snowflake.ID, data discord.SlashCommandInteractionData) error {
	pct := data.Int("percent")
	enc, setEnc := data.OptInt(optEncore)
	if !setEnc {
		enc = c.settings.Get(guild).Encore
	}
	body := fmt.Sprintf("%d%% every 5 minutes.", pct)
	if pct == 0 {
		body = "Random joins off."
	}
	if enc > 0 {
		if pct == 0 {
			body += fmt.Sprintf(" Encore kept at %d%%, inert until random joins are back on.", enc)
		} else {
			body += fmt.Sprintf(" %d%% of visits come back for a second sound.", enc)
		}
	}
	body += "\n" + block(
		meter("chance", float64(pct)/100, fmt.Sprintf("%d%%", pct)),
		meter(optEncore, float64(enc)/maxEncore, ridingValue(enc, pct)),
	)
	return c.saveThenSay(e, func() error {
		if _, err := c.settings.Update(ctx, guild, e.User().ID, func(s *settings.Settings) {
			s.Chance = pct
			if setEnc {
				s.Encore = enc
			}
		}); err != nil {
			return err
		}
		c.bus.Publish(ctx, bus.SettingsChanged{Guild: guild, Field: cmdNameChance, By: e.User().ID})
		return nil
	}, info("Drop-in chance", body))
}

func (c *Commands) cmdSuspense(ctx context.Context, e *events.ApplicationCommandInteractionCreate, guild snowflake.ID, data discord.SlashCommandInteractionData) error {
	secs := data.Int("seconds")
	fake, setFake := data.OptInt(optFakeOut)
	if !setFake {
		fake = c.settings.Get(guild).FakeOut
	}
	body := fmt.Sprintf("Up to %ds of silence before each sound.", secs)
	if secs == 0 {
		body = "Suspense off."
	}
	if fake > 0 {
		if secs == 0 {
			body += fmt.Sprintf(" Fake-out kept at %d%%, inert until suspense is back on.", fake)
		} else {
			body += fmt.Sprintf(" %d%% of visits sit it out and leave without a sound.", fake)
		}
	}
	body += "\n" + block(
		meter("suspense", float64(secs)/maxSuspense, fmt.Sprintf("%ds", secs)),
		meter(optFakeOut, float64(fake)/maxFakeOut, ridingValue(fake, secs)),
	)
	return c.saveThenSay(e, func() error {
		if _, err := c.settings.Update(ctx, guild, e.User().ID, func(st *settings.Settings) {
			st.Suspense = secs
			if setFake {
				st.FakeOut = fake
			}
		}); err != nil {
			return err
		}
		c.bus.Publish(ctx, bus.SettingsChanged{Guild: guild, Field: cmdNameSuspense, By: e.User().ID})
		return nil
	}, info("Suspense", body))
}

// maxChoices is Discord's cap on one autocomplete response.
const maxChoices = 25

// matchSounds filters names by what has been typed so far. Both sides are folded, and the query may
// match the file name or what is shown for it: "big b" and "big_b" both find big_burp.
func matchSounds(names []string, label func(string) string, q string) []discord.AutocompleteChoice {
	q = strings.ToLower(q)
	var choices []discord.AutocompleteChoice
	for _, n := range names {
		if !strings.Contains(strings.ToLower(n), q) && !strings.Contains(strings.ToLower(sounds.Display(n)), q) {
			continue
		}
		choices = append(choices, discord.AutocompleteChoiceString{Name: label(n), Value: n})
		if len(choices) == maxChoices {
			break
		}
	}
	return choices
}

// soundsPerPage keeps a page under Discord's 4096 even when every name is clipped at soundsNameMax:
// 50 lines of 51 runes, two markers, backticks and a newline is 2900.
const (
	soundsPerPage = 50
	soundsNameMax = 50
)

// clipped is a sound's label with the name cut short and the markers kept, inside a code span.
func clipped(name string, marks func(string) string) string {
	return "`" + truncate(sounds.Display(name), soundsNameMax) + marks(name) + "`"
}

// soundsPage is one page of /sounds: what /play would take, shown the way its autocomplete shows it.
func soundsPage(names []string, marks func(string) string, page int) discord.Embed {
	const title = "Sounds"
	pages := (len(names) + soundsPerPage - 1) / soundsPerPage
	switch {
	case len(names) == 0:
		return none(title, "Nothing you can ask for here.")
	case page > pages:
		return bad(fmt.Sprintf("No page %d", page), fmt.Sprintf("The last one is %d.", pages))
	}
	var sb strings.Builder
	for _, n := range names[(page-1)*soundsPerPage : min(page*soundsPerPage, len(names))] {
		sb.WriteString(clipped(n, marks) + "\n")
	}
	foot := plural(len(names), "sound", "sounds")
	if pages > 1 {
		foot += fmt.Sprintf(" · page %d of %d", page, pages)
	}
	return info(title, sb.String()).WithFooterText(foot)
}

// nowPlaying is the /play reply's sound: its emoji in place of the speaker when the app has one.
// A custom emoji does not render inside a code fence, so it cannot go in the speaker's block.
func (c *Commands) nowPlaying(guild snowflake.ID, sound string) string {
	if icon := c.soundIcon(&guild, sound); icon != "" {
		return icon + "`" + c.chars.Label(sound) + "`"
	}
	return nowPlaying(sounds.Display(sound), c.chars.Marks(sound))
}

// cmdSounds lists what the autocomplete would, past its 25: nsfw follows the caller's voice channel.
func (c *Commands) cmdSounds(_ context.Context, e *events.ApplicationCommandInteractionCreate, guild snowflake.ID, data discord.SlashCommandInteractionData) error {
	page, ok := data.OptInt(optPage)
	if !ok {
		page = 1
	}
	reg := c.character(guild).Sounds
	return e.CreateMessage(say(soundsPage(reg.Names(c.adultChannel(&guild, e.User().ID)), reg.Marks, page)))
}

// autocompleteWait is how long typing has to stop before the bot answers. Discord sends one
// interaction per keystroke and allows three seconds to answer each, so this is spent out of a
// budget nothing else is using — and it collapses a typed word into one REST call instead of one
// per letter. Long enough to swallow a burst mid-word, short enough that the list still appears
// while the user is looking at it.
const autocompleteWait = 150 * time.Millisecond

// debouncer runs the newest call per key and drops the ones it superseded. The zero value is ready.
//
// Keyed by user: two people typing must not cancel each other, and one person cannot be mid-word in
// two places in a way worth a wider key.
type debouncer struct {
	mu      sync.Mutex
	pending map[snowflake.ID]*time.Timer
}

// do schedules fn, replacing whatever was already waiting under the same key. It returns at once —
// fn runs on the timer's own goroutine, which is what keeps this callable from a gateway listener.
func (d *debouncer) do(key snowflake.ID, wait time.Duration, fn func()) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.pending == nil {
		d.pending = make(map[snowflake.ID]*time.Timer)
	}
	if prev, ok := d.pending[key]; ok {
		prev.Stop()
	}
	// Declared before it is assigned so the timer's own func can tell whether it is still the
	// pending one: Stop loses the race against a timer already firing, and without this the loser
	// would delete the entry its replacement had just written.
	var t *time.Timer
	t = time.AfterFunc(wait, func() {
		d.mu.Lock()
		if d.pending[key] == t {
			delete(d.pending, key)
		}
		d.mu.Unlock()
		fn()
	})
	d.pending[key] = t
}

// A /play holds the user for playUserCooldown and the guild for playGuildCooldown: the first stops
// one person chaining plays, the second stops several tag-teaming one.
const (
	playUserCooldown  = 30 * time.Second
	playGuildCooldown = 10 * time.Second
)

// cooldown tracks when each user and guild may next /play. The zero value is ready.
type cooldown struct {
	mu     sync.Mutex
	now    func() time.Time
	users  map[snowflake.ID]time.Time
	guilds map[snowflake.ID]time.Time
}

// take starts both cooldowns and reports true, or starts neither and reports when the later of the
// two running ones ends. Check and record are one step so two concurrent /plays cannot both pass.
func (c *cooldown) take(guild, user snowflake.ID) (time.Time, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	if c.now != nil {
		now = c.now()
	}
	if c.users == nil {
		c.users = make(map[snowflake.ID]time.Time)
		c.guilds = make(map[snowflake.ID]time.Time)
	}
	expired := func(_ snowflake.ID, until time.Time) bool { return !now.Before(until) }
	maps.DeleteFunc(c.users, expired)
	maps.DeleteFunc(c.guilds, expired)

	next := c.users[user]
	if g := c.guilds[guild]; g.After(next) {
		next = g
	}
	if now.Before(next) {
		return next, false
	}
	c.users[user] = now.Add(playUserCooldown)
	c.guilds[guild] = now.Add(playGuildCooldown)
	return time.Time{}, true
}

// onAutocomplete answers the last keystroke of a burst and lets the earlier ones go unanswered.
// Discord shows whatever arrived most recently and says nothing about a response that never came,
// so a superseded query costs a list that was already stale, not an error the user can see.
func (c *Commands) onAutocomplete(e *events.AutocompleteInteractionCreate) {
	c.autocomplete.do(e.User().ID, autocompleteWait, func() {
		defer logPanic("autocomplete")
		reg := c.chars.Default().Sounds
		if g := e.GuildID(); g != nil {
			reg = c.character(*g).Sounds
		}
		if err := e.AutocompleteResult(matchSounds(reg.Names(c.adultChannel(e.GuildID(), e.User().ID)), reg.Label, e.Data.String("sound"))); err != nil {
			slog.Error("autocomplete", slog.Any("err", err))
		}
	})
}

// adultChannel reports whether the voice channel user sits in may hear nsfw sounds.
func (c *Commands) adultChannel(guild *snowflake.ID, user snowflake.ID) bool {
	if guild == nil {
		return false
	}
	vs, ok := c.client.Caches.VoiceState(*guild, user)
	return ok && vs.ChannelID != nil && c.settings.Get(*guild).NSFW.Allows(voice.AgeRestricted(c.client, *guild, *vs.ChannelID))
}

// playChannel resolves where a /play from user would land: the channel, or the embed saying why
// it cannot. Every refusal names its own reason, so a channel the bot simply does not support is
// not reported as a permission problem.
func (c *Commands) playChannel(guild, user snowflake.ID) (snowflake.ID, *discord.Embed) {
	vs, ok := c.client.Caches.VoiceState(guild, user)
	if !ok || vs.ChannelID == nil {
		return 0, deny("Not in a voice channel", "Join one first.")
	}
	ch, ok := c.client.Caches.Channel(*vs.ChannelID)
	if !ok {
		return 0, deny("Can't speak there", "I don't have the permissions for that channel.")
	}
	switch {
	case ch.Type() == discord.ChannelTypeGuildStageVoice:
		return 0, deny("Stage channels aren't supported", "Discord seats me in the audience there, where nobody can hear me.")
	case len(voice.Humans(c.client, guild, ch.ID())) == 0:
		return 0, deny("Nobody can hear it", "Everyone in that channel is deafened.")
	case len(voice.Usable(c.client, guild, ch)) == 0:
		return 0, deny("Can't speak there", "I don't have the permissions for that channel.")
	}
	return *vs.ChannelID, nil
}

func (c *Commands) cmdPlay(ctx context.Context, e *events.ApplicationCommandInteractionCreate, guild snowflake.ID, data discord.SlashCommandInteractionData) error {
	if c.character(guild).ID == "" {
		return e.CreateMessage(say(bad("No character yet", noCharacter)))
	}
	channel, refusal := c.playChannel(guild, e.User().ID)
	if refusal != nil {
		return e.CreateMessage(say(*refusal))
	}
	nsfw := c.settings.Get(guild).NSFW.Allows(voice.AgeRestricted(c.client, guild, channel))
	reg := c.character(guild).Sounds
	sound, given := data.OptString("sound")
	if !given {
		var ok bool
		if sound, ok = reg.Pick(nsfw); !ok {
			return e.CreateMessage(say(bad("No sounds loaded", "There is nothing to play.")))
		}
	} else if !reg.Playable(sound, nsfw) {
		return e.CreateMessage(say(bad("No such sound", "Pick one from the autocomplete.")))
	}
	if voice.Busy(guild) {
		return e.CreateMessage(say(bad("Already busy here", "Let the current one finish.")))
	}
	if next, ok := c.plays.take(guild, e.User().ID); !ok {
		return e.CreateMessage(say(bad("Slow down", fmt.Sprintf("The next play is allowed <t:%d:R>.", next.Unix()))))
	}
	name, _ := c.self(guild)
	body := c.nowPlaying(guild, sound) + playAd(c.siblings, c.client.ApplicationID, name, rand.IntN)
	if err := e.CreateMessage(say(info("Playing", body))); err != nil {
		return err
	}
	c.play(ctx, PlayArgs{Guild: guild, Channel: channel, User: e.User().ID, Sound: sound})
	return nil
}

// commandList renders the commands from definitions rather than from a written-out copy, so a
// command added above shows up here without anyone remembering to. Name and description are
// already the user-facing strings Discord shows; the permission is the only thing that needs
// rendering, and Permissions.String does that ("Manage Server").
//
// Owner-only entries are annotated in their own option descriptions rather than hidden, which
// keeps this free of any branch on who is asking.
func commandList(defs []discord.ApplicationCommandCreate) string {
	var sb strings.Builder
	for _, d := range defs {
		c, ok := d.(discord.SlashCommandCreate)
		if !ok {
			continue // nothing but slash commands is registered; a context-menu entry has no description
		}
		fmt.Fprintf(&sb, "`/%s` — %s", c.Name, c.Description)
		if p := c.DefaultMemberPermissions; !p.IsZero() && p.Value != nil {
			fmt.Fprintf(&sb, " · *%s*", p.Value.String())
		}
		sb.WriteByte('\n')
	}
	return sb.String()
}

// The three ways to limit the bot, kept apart because conflating them is how an admin denies the
// wrong thing and concludes the bot is broken. Static: none of it is derivable from definitions.
const helpLimits = "Three different switches, and they do not do the same thing:\n" +
	"• **Deny the bot Connect** on a voice channel (channel permissions) — it will never drop in there. " +
	"This is the one that decides *where* it goes.\n" +
	"• **Server Settings → Integrations** — decides *who* may run these commands. It does not stop drop-ins.\n" +
	"• `/optout on` — per person: the bot stops counting you when it picks a channel, so a channel with " +
	"only you in it is left alone. It still joins channels where other people are, and you will hear it there. " +
	"`/optout for <hours>` is the same with an end to it, `/optout schedule` repeats it every week, " +
	"and `/optout off` ends any of them."

// helpNSFW is the three /nsfw modes, for the admins reading /help to find where 18+ sounds are set.
const helpNSFW = "Admins choose where with `/nsfw`: **off**, **restricted** (wherever Discord has " +
	"age-restricted the server or the voice channel, the default) or **on** (every voice channel; it " +
	"asks first and records who turned it on)."

const helpSounds = "`/sounds` lists everything `/play` will take from you, a page at a time. The autocomplete stops at 25."

// cmdHelp is one embed per subject, so each title is a heading someone scrolling on a phone can
// find: the commands under the bot's own name and avatar, the switches, the sounds, then its friends.
func (c *Commands) cmdHelp(_ context.Context, e *events.ApplicationCommandInteractionCreate, guild snowflake.ID, _ discord.SlashCommandInteractionData) error {
	name, avatar := c.self(guild)
	commands := info(withEmoji(c.character(guild).Emoji, name), commandList(c.Definitions()))
	if avatar != "" {
		commands = commands.WithThumbnail(avatar)
	}
	fixed := []discord.Embed{
		commands,
		info("Keeping the bot out", helpLimits),
		info("Sounds", helpSounds+"\n\n"+adultHelp(c.settings.Get(guild).NSFW, voice.AgeRestrictedGuild(c.client, guild))+"\n"+helpNSFW),
	}
	if c.note != "" {
		fixed = append(fixed, info("", c.note))
	}
	return e.CreateMessage(say(fitHelp(fixed, friendsGrid(c.siblings, c.client.ApplicationID, name))...))
}

// adultHelp says whether and where 18+ sounds can play in this server: the mode /nsfw sets, and
// under restricted, whether any voice channel carries Discord's age-restricted label.
func adultHelp(mode settings.NSFW, labelled bool) string {
	switch {
	case mode == settings.NSFWOff:
		return "**18+:** Off here. An admin turned them off with `/nsfw`."
	case mode == settings.NSFWOn:
		return "**18+:** On in every voice channel here. An admin turned that on with `/nsfw`."
	case labelled:
		return "**18+:** On here, wherever Discord has age-restricted the server or the voice channel."
	}
	return "**18+:** Off here. Neither the server nor any of its voice channels is age-restricted in Discord."
}

// fitHelp drops friends from the end of the grid until the reply fits, and the grid itself only
// once it is down to one; the fixed embeds always go out.
func fitHelp(fixed, friends []discord.Embed) []discord.Embed {
	out := slices.Concat(fixed, friends)
	for len(out) > len(fixed) && messageSize(out) > messageLimit {
		if last := &out[len(out)-1]; len(last.Fields) > 1 {
			last.Fields = last.Fields[:len(last.Fields)-1]
			continue
		}
		out = out[:len(out)-1]
	}
	return out
}

func messageSize(es []discord.Embed) int {
	n := 0
	for _, e := range es {
		n += embedSize(e)
	}
	return n
}

// withEmoji puts the profile's emoji in front of a title, when it has one.
func withEmoji(emoji, title string) string {
	if emoji == "" {
		return title
	}
	return emoji + " " + title
}

// aboutLimit keeps /about under Discord's 4096 description cap with room for the ellipsis; the lore is
// the operator's free text and nothing else bounds it.
const aboutLimit = 4000

// aboutBody is the character sheet: tagline, lore, then traits as a list, each left out when the
// profile has none of it. The operator's note goes under it, story or not.
func aboutBody(p profile.Profile, note string) string {
	var parts []string
	if p.Tagline != "" {
		parts = append(parts, "*"+p.Tagline+"*")
	}
	if p.Lore != "" {
		parts = append(parts, p.Lore)
	}
	if len(p.Traits) > 0 {
		parts = append(parts, "**Traits**\n• "+strings.Join(p.Traits, "\n• "))
	}
	if len(parts) == 0 {
		parts = append(parts, "No story yet.")
	}
	if note != "" {
		parts = append(parts, note)
	}
	return truncate(strings.Join(parts, "\n\n"), aboutLimit)
}

func (c *Commands) cmdAbout(_ context.Context, e *events.ApplicationCommandInteractionCreate, guild snowflake.ID, _ discord.SlashCommandInteractionData) error {
	name, avatar := c.self(guild)
	ch := c.character(guild)
	body := aboutBody(ch.Profile, c.note)
	if ch.ID == "" {
		body = strings.TrimSpace(noCharacter + "\n\n" + body)
	}
	return e.CreateMessage(say(info(withEmoji(ch.Emoji, name), body).WithThumbnail(avatar)))
}

// self is the bot as people in guild see it, and the avatar that goes with it. The profile's
// nickname wins, because the character's name is the operator's to decide; then its nickname in that
// server; then its own name. coucou is the application, never the character, so nothing here names it.
func (c *Commands) self(guild snowflake.ID) (name, avatar string) {
	name = "the bot"
	if m, ok := c.client.Caches.SelfMember(guild); ok {
		name, avatar = m.EffectiveName(), m.EffectiveAvatarURL()
	} else if u, ok := c.client.Caches.SelfUser(); ok {
		name, avatar = u.EffectiveName(), u.EffectiveAvatarURL()
	}
	return cmp.Or(c.character(guild).Nickname, name), avatar
}

// markdown escapes what would restyle or break a name dropped into bold or a link label: a
// nickname is set by the server's admins, not by whoever runs the bot.
var markdown = strings.NewReplacer(`\`, `\\`, "*", `\*`, "_", `\_`, "~", `\~`, "`", "\\`", "|", `\|`, "[", `\[`, "]", `\]`)

// playAdOdds is 1 in how many /play replies plug a sibling: rare enough to read as a wink rather
// than an ad, and only on a reply someone asked for, never on a visit nobody did.
const playAdOdds = 100

// playAd is the occasional line under a /play reply pointing at one random sibling. It is empty on
// every other roll, and whenever there is nobody but this bot to point at.
func playAd(siblings []Sibling, self snowflake.ID, name string, intN func(int) int) string {
	if intN(playAdOdds) != 0 {
		return ""
	}
	others := make([]Sibling, 0, len(siblings))
	for _, s := range siblings {
		if s.App != self {
			others = append(others, s)
		}
	}
	if len(others) == 0 {
		return ""
	}
	s := others[intN(len(others))]
	return "\n*Psst... " + markdown.Replace(name) + " has friends. Try [" + s.Name + "](" + inviteURL(s.App) + ").*"
}

// maxFriends is Discord's cap on fields in one embed; a friend past it is left off rather than the
// whole reply being rejected.
const maxFriends = 25

// friendsGrid is one "Friends of <name>" embed with a field per sibling but this bot: its name and
// an invite, inline, so they sit side by side as a grid. Nothing when there is nobody else.
func friendsGrid(siblings []Sibling, self snowflake.ID, name string) []discord.Embed {
	grid := info("Friends of "+name, "")
	for _, s := range siblings {
		if s.App != self && len(grid.Fields) < maxFriends {
			grid = grid.AddField(s.Name, "[Add to a server]("+inviteURL(s.App)+")", true)
		}
	}
	if len(grid.Fields) == 0 {
		return nil
	}
	return []discord.Embed{grid}
}

// inviteURL is the OAuth2 link that adds the bot to a server, asking for voice.Needed and nothing
// else. Both scopes are required and neither is optional: bot adds the user, applications.commands
// is what makes the slash commands appear — added without it the bot joins and answers nothing.
//
// The permission bits go over as an integer. discord.QueryValues renders every value through
// fmt.Sprint, and Permissions has a String method, so passing the constant itself would ask
// Discord to authorize "View Channel, Connect, Speak" and be rejected.
func inviteURL(app snowflake.ID) string {
	return discord.AuthorizeURL(discord.QueryValues{
		"client_id":   app,
		"permissions": int64(voice.Needed),
		"scope":       "bot applications.commands",
	})
}

// inviteWhy names each permission and what breaks without it, because the person pasting this link
// is an admin deciding whether to trust it, and an unexplained tick box is what gets denied.
const inviteWhy = "**What it asks for**\n" +
	"• **View Channel** — to see that a voice channel exists at all.\n" +
	"• **Connect** — to join it.\n" +
	"• **Speak** — to make the noise. That is the entire point.\n\n" +
	"Nothing else: it cannot read your messages, and it never joins a channel that denies it any of the three."

func (c *Commands) cmdInvite(_ context.Context, e *events.ApplicationCommandInteractionCreate, guild snowflake.ID, _ discord.SlashCommandInteractionData) error {
	name, avatar := c.self(guild)
	body := "[**Add " + markdown.Replace(name) + " to a server**](" + inviteURL(c.client.ApplicationID) + ")\n\n" + inviteWhy
	return e.CreateMessage(say(info("Invite", body).WithThumbnail(avatar)))
}

// optOutTerms is the half of every opt-out reply that does not change with the subcommand.
const optOutTerms = "The bot will not pick a channel just because you are in it, and will leave a channel alone if you are the only one there.\n\n" +
	"It does not make you inaudible: if other people are in the channel the bot still drops in, and you will still hear it."

// silenceText is what /optout and /quiet say differently; the shapes, the checks and the writes
// are shared. timed is a format for the end's unix time, and terms closes every reply that turns
// one on.
type silenceText struct {
	cmd                      string
	on, timed, sched, off    string
	onBody, timedBody, offOK string
	terms                    string
}

var optOutText = silenceText{
	cmd: cmdNameOptOut, on: "Not counting you", timed: "Not counting you for now", sched: "Not counting you on a schedule", off: "Counting you again",
	onBody:    "Until you say otherwise — `/optout for` is the same thing with an end to it, and `/optout off` puts you back.",
	timedBody: "You are back in <t:%d:R>. `/optout off` ends it sooner, `/optout on` drops the end date.",
	offOK:     "You are back in. The bot can pick a channel because you are sitting in it.",
	terms:     "\n\n" + optOutTerms,
}

var quietText = silenceText{
	cmd: cmdNameQuiet, on: "Quiet", timed: "Quiet for now", sched: "Quiet on a schedule", off: "Not quiet",
	onBody:    "Nothing drops in until someone says otherwise — `/quiet for` is the same with an end to it, and `/quiet off` lifts it.",
	timedBody: "Nothing drops in until <t:%d:R>. `/quiet off` ends it sooner, `/quiet on` drops the end date.",
	offOK:     "Off. No rest for anyone.",
}

// cmdOptOut is per-user and bot-wide, so it carries no DefaultMemberPermissions and writes nothing
// against the guild it was run in. It still reads one thing from it: a schedule needs a time zone,
// and the guild's is the only one the bot has — a person carries none it can see.
//
// Every reply spells the semantics out because they are deliberately partial and would otherwise
// read as broken: opting out keeps a room that holds only opted-out people from being picked, but
// it does not follow you into a room where other people are present.
func (c *Commands) cmdOptOut(ctx context.Context, e *events.ApplicationCommandInteractionCreate, guild snowflake.ID, data discord.SlashCommandInteractionData) error {
	return c.silence(ctx, e, guild, data, c.optouts, e.User().ID, optOutText, nil)
}

// cmdQuiet keeps what /quiet set always did with a zone: it becomes the server's.
func (c *Commands) cmdQuiet(ctx context.Context, e *events.ApplicationCommandInteractionCreate, guild snowflake.ID, data discord.SlashCommandInteractionData) error {
	by := e.User().ID
	return c.silence(ctx, e, guild, data, c.quiet, guild, quietText, func(tz string) error {
		if tz != "" {
			if _, err := c.settings.Update(ctx, guild, by, func(s *settings.Settings) { s.TZ = &tz }); err != nil {
				return err
			}
		}
		c.bus.Publish(ctx, bus.SettingsChanged{Guild: guild, Field: cmdNameQuiet, By: by})
		return nil
	})
}

// silence is both commands. saved, when set, runs after a successful write with the zone the
// person typed, or "" when they typed none.
func (c *Commands) silence(
	ctx context.Context,
	e *events.ApplicationCommandInteractionCreate,
	guild snowflake.ID,
	data discord.SlashCommandInteractionData,
	s *silence.Store,
	id snowflake.ID,
	t silenceText,
	saved func(tz string) error,
) error {
	sub := optOn
	if data.SubCommandName != nil {
		sub = *data.SubCommandName
	}
	write := func(w func() error, tz string) func() error {
		return func() error {
			if err := w(); err != nil || saved == nil {
				return err
			}
			return saved(tz)
		}
	}
	if sub == optOff {
		return c.saveThenSay(e, write(func() error { return s.Clear(ctx, id) }, ""), info(t.off, t.offOK))
	}

	row := store.Silence{ID: id, By: e.User().ID}
	var tz string
	var reply discord.Embed
	switch sub {
	case optFor:
		// A real instant rather than a duration the store would have to interpret. Rendered
		// relative and in the reader's own zone; the bot knows no zone for a person.
		until := time.Now().Add(time.Duration(data.Int(fieldHours)) * time.Hour)
		row.Until = &until
		reply = info(t.timed, fmt.Sprintf(t.timedBody, until.Unix())+t.terms)
	case optSchedule, optRule:
		spec, desc := data.String(fieldRule), "`"+data.String(fieldRule)+"`"
		if sub == optSchedule {
			from := data.Int(fieldFrom)
			spec = weeklySpec(data.String(fieldDays), from)
			desc = fmt.Sprintf("%s at %02d:00", dayLabel(data.String(fieldDays)), from)
		}
		zone, ok := data.OptString(fieldTZ)
		if ok {
			tz = zone
		} else {
			zone = settings.Zone(c.settings.Get(guild))
		}
		now := time.Now()
		r, err := buildRule(spec, zone, now)
		if err != nil {
			return e.CreateMessage(say(bad("That schedule will not work", err.Error())))
		}
		hours := data.Int(fieldHours)
		row.Rule, row.Window = r.String(), time.Duration(hours)*time.Hour
		reply = info(t.sched, fmt.Sprintf("%s, %s at a time, %s.\nThe next one starts <t:%d:R>, and `/%s off` ends the whole thing.",
			desc, plural(hours, "hour", "hours"), zone, r.After(now, true).Unix(), t.cmd)+t.terms)
	default:
		reply = info(t.on, t.onBody+t.terms)
	}
	return c.saveThenSay(e, write(func() error { return s.Set(ctx, row) }, tz), reply)
}

func (c *Commands) cmdChaos(ctx context.Context, e *events.ApplicationCommandInteractionCreate, guild snowflake.ID, data discord.SlashCommandInteractionData) error {
	by := e.User().ID
	if data.SubCommandName != nil && *data.SubCommandName == optOff {
		return c.saveThenSay(e, func() error { return c.chaos.Set(ctx, store.Chaos{Guild: guild, CreatedBy: by}) },
			info("Chaos", "Off. Back to the usual odds."))
	}
	st := c.settings.Get(guild)
	chance := data.Int(fieldChance)
	if refusal := chaosRefusal(chance, st.Chance); refusal != nil {
		return e.CreateMessage(say(*refusal))
	}
	tz, ok := data.OptString(fieldTZ)
	if !ok {
		tz = settings.Zone(st)
	}
	days, from, hours := data.String(fieldDays), data.Int(fieldFrom), data.Int(fieldHours)
	now := time.Now()
	r, err := buildRule(weeklySpec(days, from), tz, now)
	if err != nil {
		return e.CreateMessage(say(bad("That window will not work", err.Error())))
	}
	w := schedule.Window{Rule: r, Length: time.Duration(hours) * time.Hour}
	body := fmt.Sprintf("%s, %d%% instead of %d%%.\nThe next one starts <t:%d:R>.",
		chaosLabel(days, from, hours, tz), chance, st.Chance, r.After(now, true).Unix())
	if q, ok := c.quiet.Get(guild); ok && quietOverlap(w, q.At, now) {
		body += "\n\nPart of it falls in quiet time, and quiet wins: nothing drops in then."
	}
	return c.saveThenSay(e, func() error {
		return c.chaos.Set(ctx, store.Chaos{Guild: guild, Rule: r.String(), Hours: hours, Chance: chance, CreatedBy: by})
	}, info("Chaos", body))
}

// chaosRefusal is nil when chance is a boost over base.
func chaosRefusal(chance, base int) *discord.Embed {
	if chance > base {
		return nil
	}
	return deny("Not chaos", fmt.Sprintf("That is no higher than the usual %d%%. Pick more than that, or raise `/chance` instead.", base))
}

// quietOverlap walks the week ahead an hour at a time. Both windows start on the hour, so hourly
// samples cannot miss an intersection.
func quietOverlap(w schedule.Window, quiet func(time.Time) bool, now time.Time) bool {
	start := now.Truncate(time.Hour)
	for h := range 7 * 24 {
		t := start.Add(time.Duration(h) * time.Hour)
		w.At(t)
		if w.Active(t) && quiet(t) {
			return true
		}
	}
	return false
}

func chaosLabel(days string, from, hours int, zone string) string {
	return fmt.Sprintf("%s from %02d:00 for %s, %s", dayLabel(days), from, plural(hours, "hour", "hours"), zone)
}

// scheduleLabel reads a window back out of its stored rule, which is all the tables keep: the
// guided weekly form in words, anything else as the rule itself.
func scheduleLabel(w schedule.Window) string {
	hours := int(w.Length / time.Hour)
	zone := w.Rule.GetDTStart().Location().String()
	o := w.Rule.OrigOptions
	if o.Freq != rrule.WEEKLY || len(o.Byweekday) == 0 {
		return fmt.Sprintf("`%s` for %s, %s", o.RRuleString(), plural(hours, "hour", "hours"), zone)
	}
	days, from := scheduleDays(w)
	return chaosLabel(days, from, hours, zone)
}

func scheduleDays(w schedule.Window) (days string, from int) {
	o := w.Rule.OrigOptions
	d := make([]string, len(o.Byweekday))
	for i, wd := range o.Byweekday {
		d[i] = wd.String()
	}
	if len(o.Byhour) > 0 {
		from = o.Byhour[0]
	}
	return strings.Join(d, ","), from
}

func (c *Commands) cmdStatus(_ context.Context, e *events.ApplicationCommandInteractionCreate, guild snowflake.ID, _ discord.SlashCommandInteractionData) error {
	st := c.settings.Get(guild)
	// Both counts: a bar needs a denominator, and "in earshot" only means something against how
	// many voice channels there are. The cache holds voice and stage channels and nothing else.
	populated, rooms := 0, 0
	for ch := range c.client.Caches.ChannelsForGuild(guild) {
		rooms++
		if len(voice.Usable(c.client, guild, ch)) > 0 {
			populated++
		}
	}
	// Each meter value is a phrase, not a bare number, so the panel still reads correctly when a
	// screen reader linearises the block and the bars become nothing at all.
	susp := shownOff
	if st.Suspense > 0 {
		susp = fmt.Sprintf("≤%ds", st.Suspense)
	}
	chance := "never"
	if st.Chance > 0 {
		chance = fmt.Sprintf("%d%% /5min", st.Chance)
	}
	body := panel("status",
		meter("chance", float64(st.Chance)/100, chance),
		meter("suspense", float64(st.Suspense)/maxSuspense, susp),
		meter(optFakeOut, float64(st.FakeOut)/maxFakeOut, ridingValue(st.FakeOut, st.Suspense)),
		meter(optEncore, float64(st.Encore)/maxEncore, ridingValue(st.Encore, st.Chance)),
		meter("earshot", frac(populated, rooms), fmt.Sprintf("%d/%s", populated, plural(rooms, "room", "rooms"))),
	)
	// Quiet stays outside the block: its window is the one setting whose shape is worth drawing,
	// and the words above it are what a screen reader is left with.
	now := time.Now()
	chaosLine := "Chaos: off."
	if w, ok := c.chaos.Get(guild); ok {
		chaosLine = fmt.Sprintf("Chaos: %s, at %d%%", scheduleLabel(w.Window), w.Chance)
		if w.Active(now) {
			chaosLine += activeNow
		}
	}
	return e.CreateMessage(say(info("Status", body+"\n"+chaosLine+"\n"+c.quietLine(guild, now))))
}

// quietLine is the status readout's quiet: in words, then the day drawn when it has a schedule.
func (c *Commands) quietLine(guild snowflake.ID, now time.Time) string {
	q, ok := c.quiet.Get(guild)
	if !ok {
		return "Quiet: off — no rest for anyone."
	}
	quiet := "Quiet: all the time"
	if q.Rule != nil {
		quiet = "Quiet: " + scheduleLabel(q.Window)
	}
	if !q.Until.IsZero() {
		quiet += fmt.Sprintf(", until <t:%d:f>", q.Until.Unix())
	}
	if q.On(now) {
		quiet += activeNow
	}
	if q.Rule != nil {
		quiet += "\n" + hourStrip(today(q, now))
	}
	return quiet
}

// today is each hour of the day in the rule's own zone, on or off: what the hour strip draws.
func today(q silence.Entry, now time.Time) []bool {
	loc := q.Rule.GetDTStart().Location()
	y, m, d := now.In(loc).Date()
	cells := make([]bool, dayHours)
	for h := range cells {
		cells[h] = q.At(time.Date(y, m, d, h, 0, 0, 0, loc))
	}
	return cells
}

// ridingValue says when a percentage is kept but cannot happen: a fake-out rides on suspense and an
// encore on chance, so with its host off the stored value waits rather than being lost.
func ridingValue(pct, host int) string {
	switch {
	case pct == 0:
		return shownOff
	case host == 0:
		return fmt.Sprintf("%d%% inert", pct)
	}
	return fmt.Sprintf("%d%%", pct)
}

const (
	noneYet  = "none yet"
	shownOff = "off"
)

func pct(v *float64) string {
	if v == nil {
		return noneYet
	}
	return fmt.Sprintf("%.0f%%", *v*100)
}

// orNone is a top sound in guild, or bot-wide for nil.
func (c *Commands) orNone(guild *snowflake.ID, s *string) string {
	if s == nil {
		return noneYet
	}
	return c.soundIcon(guild, *s) + "`" + c.chars.Label(*s) + "`"
}

func (c *Commands) cmdStats(ctx context.Context, e *events.ApplicationCommandInteractionCreate, guild snowflake.ID, data discord.SlashCommandInteractionData) error {
	scope := data.String(fieldScope)
	if err := e.DeferCreateMessage(statsEphemeral(scope)); err != nil {
		return err
	}
	switch scope {
	case scopeGuild:
		return c.statsGuild(ctx, e, guild)
	case scopeBot:
		return c.statsBot(ctx, e)
	default:
		return c.statsUser(ctx, e, guild)
	}
}

// statsEphemeral keeps a person's own numbers to themselves; the server's and the bot's are for
// the room.
func statsEphemeral(scope string) bool { return scope != scopeGuild && scope != scopeBot }

// statsBot answers anyone: what the bot has been up to across every server is the sort of number a
// bot listing prints on its own page, and hiding it bought nothing. Drawn in UTC: the bot has no
// zone of its own, and any one server's would be a guess for every other.
func (c *Commands) statsBot(ctx context.Context, e *events.ApplicationCommandInteractionCreate) error {
	w := lastDays(time.Now(), time.UTC, trendDays)
	var s store.GlobalStats
	var hours []store.PlayHour
	var who []store.Row
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() (err error) { s, err = c.events.GlobalStats(gctx); return err })
	g.Go(func() (err error) { hours, err = c.events.PlaysHourly(gctx, nil, w.start); return err })
	g.Go(func() (err error) { who, err = c.events.CharacterPlays(gctx, nil); return err })
	if err := g.Wait(); err != nil {
		return c.failed(e, "Couldn't count", err)
	}
	// No empty variant: somebody running this on a dead bot wants to see the zeros.
	return c.edit(e, fit(withByCharacter(info("Bot stats", playsReport(w, hours, "UTC")).
		WithTimestamp(time.Now()).
		AddField("Plays (24h)", strconv.Itoa(s.Plays24h), true).
		AddField("Servers (24h)", strconv.Itoa(s.Guilds24h), true).
		AddField("Top sound (7 days)", c.orNone(nil, s.TopSound), false), c.byCharacter(who)))...)
}

func hourAt(h store.PlayHour) time.Time { return h.Hour }
func playsOf(h store.PlayHour) int      { return h.Plays }

// playsReport is the drawn half of a guild's or the bot's stats: the week in words, a month of
// days, the week by hour in zone, and how the visits came about and ended.
func playsReport(w calendar, hours []store.PlayHour, zone string) string {
	plays := daily(w, hours, hourAt, playsOf)
	cur, prev := weeks(plays)
	var t store.PlayHour
	for _, h := range hours {
		if w.day(h.Hour) < 0 {
			continue
		}
		t.Loops, t.Commands, t.Encores = t.Loops+h.Loops, t.Commands+h.Commands, t.Encores+h.Encores
		t.Plays, t.FakeOuts, t.Failed = t.Plays+h.Plays, t.FakeOuts+h.FakeOuts, t.Failed+h.Failed
	}
	out := fmt.Sprintf("**%s** this week, %s.\n", plural(cur, "play", "plays"), trend(cur, prev)) +
		titled("plays a day", columns(plays, w.label(0), w.label(w.n-1))...) +
		titled("when it strikes · "+zone, heatmap(weekHours(w, hours, hourAt, playsOf))...)
	how := split([]part{{labelLoop, t.Loops}, {labelPlay, t.Commands}, {optEncore, t.Encores}}, splitWidth)
	ended := split([]part{{"played", t.Plays}, {"fake-out", t.FakeOuts}, {"failed", t.Failed}}, splitWidth)
	switch {
	case how != nil && ended != nil:
		out += titled("how visits began and ended · 30 days", append(append(how, ""), ended...)...)
	case ended != nil:
		out += titled("how visits ended · 30 days", ended...)
	}
	return out
}

// standing is one ranked number: pct is the whole percent of its population strictly below it, and
// long is a format taking pct and the population's name.
type standing struct {
	short, long string
	pct         int
}

// rankText puts the best standing first in full and the rest after it in short. Nothing beats a
// 0%, so those are left out, and "" means there is no rank worth a line.
func rankText(ss []standing, of string) string {
	best := -1
	for i, s := range ss {
		if s.pct > 0 && (best < 0 || s.pct > ss[best].pct) {
			best = i
		}
	}
	if best < 0 {
		return ""
	}
	// Past 99 the cut-points are stale: nobody beats all of a population that includes them.
	out := fmt.Sprintf(ss[best].long, min(ss[best].pct, 99), of)
	var rest []string
	for i, s := range ss {
		if i != best && s.pct > 0 {
			rest = append(rest, fmt.Sprintf("%s %d%%", s.short, min(s.pct, 99)))
		}
	}
	if len(rest) > 0 {
		out += "\n" + strings.Join(rest, " · ")
	}
	return out + "\n"
}

// rankFunc is where a value's standing comes from: exact for a guild's people, cut-points for
// everyone else. false means there is nothing to compare against yet.
type rankFunc func(metric string, v float64) (int, bool)

// userStandings is nil until the person was caught at least once in the window; before that they
// are in no population.
func userStandings(n store.UserCounts, rank rankFunc) []standing {
	if n.Heard == 0 {
		return nil
	}
	var out []standing
	for _, m := range []struct {
		metric, short, long string
		v                   int
	}{
		{store.MetricHeard, "caught", "Caught more often than **%d%%** of %s.", n.Heard},
		{store.MetricTriggered, labelPlay, "Ran /play more than **%d%%** of %s.", n.Triggered},
		{store.MetricFled, "fled", "Fled more often than **%d%%** of %s.", n.Fled},
	} {
		if p, ok := rank(m.metric, float64(m.v)); ok {
			out = append(out, standing{short: m.short, long: m.long, pct: p})
		}
	}
	return out
}

// guildStandings is nil until the guild has a play in the window.
func guildStandings(r store.GuildRecent, rank rankFunc) []standing {
	if r.Plays == 0 {
		return nil
	}
	var out []standing
	if p, ok := rank(store.MetricPlays, float64(r.Plays)); ok {
		out = append(out, standing{short: "busier", long: "Busier than **%d%%** of %s.", pct: p})
	}
	if p, ok := rank(store.MetricListeners, r.AvgListeners); ok {
		out = append(out, standing{short: "listeners/visit", long: "More listeners per visit than **%d%%** of %s.", pct: p})
	}
	return out
}

// exactRank ranks against a guild's people, which the query counted on read.
func exactRank(r store.UserRank) rankFunc {
	below := map[string]int{store.MetricHeard: r.HeardBelow, store.MetricTriggered: r.TriggeredBelow, store.MetricFled: r.FledBelow}
	return func(metric string, _ float64) (int, bool) {
		if r.Of == 0 {
			return 0, false
		}
		return below[metric] * 100 / r.Of, true
	}
}

// statsUser is the only report about the person asking rather than the server. Either side failing
// fails the reply: both read the same database, so half an answer would mostly be a coincidence.
func (c *Commands) statsUser(ctx context.Context, e *events.ApplicationCommandInteractionCreate, guild snowflake.ID) error {
	user := e.User().ID
	loc, zone := c.zone(guild)
	w := lastDays(time.Now(), loc, trendDays)
	var here, everywhere discord.Embed
	var nowhere bool
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() (err error) {
		here, _, err = c.userReport(gctx, guild, user, w, zone, false)
		return err
	})
	g.Go(func() (err error) {
		everywhere, nowhere, err = c.userReport(gctx, guild, user, w, zone, true)
		return err
	})
	if err := g.Wait(); err != nil {
		return c.failed(e, "Couldn't count", err)
	}
	if nowhere {
		return c.edit(e, fit(everywhere)...)
	}
	return c.edit(e, fit(here, everywhere)...)
}

func userAt(h store.UserHour) time.Time { return h.Hour }
func heardOf(h store.UserHour) int      { return h.Heard }

// userReport is one side of /stats user, and empty when nothing is on the user there yet. Heard is
// the number that matters: being in the room when the bot turned up is the thing that happens to you.
func (c *Commands) userReport(ctx context.Context, guild, user snowflake.ID, w calendar, zone string, global bool) (discord.Embed, bool, error) {
	where, title, of, here := &guild, "Here", "people in this server", " here"
	if global {
		where, title, of, here = nil, "Everywhere", "people across every server", ""
	}
	s, err := c.events.UserStats(ctx, where, user)
	if err != nil {
		return discord.Embed{}, false, err
	}
	if s.Heard == 0 && s.Triggered == 0 && s.Fled == 0 {
		return none("Nothing on you"+here+" yet", "The bot has never turned up while you were in a channel"+here+". Enjoy it while it lasts."), true, nil
	}
	heard, err := c.events.HeardSounds(ctx, where, user)
	if err != nil {
		return discord.Embed{}, false, err
	}
	standings, err := c.rankUser(ctx, guild, user, global)
	if err != nil {
		return discord.Embed{}, false, err
	}
	hours, err := c.events.UserHourly(ctx, where, user, w.start)
	if err != nil {
		return discord.Embed{}, false, err
	}
	last := noneYet
	if s.LastHeard != nil {
		// Discord renders this relative and in the reader's own zone, which is the only way to
		// print a timestamp without picking a time zone on somebody's behalf.
		last = fmt.Sprintf("<t:%d:R>", s.LastHeard.Unix())
	}
	top := max(s.Heard, s.Triggered, s.Fled)
	col := c.character(guild).Sounds.Collection(heard, c.settings.Get(guild).NSFW.Allows(voice.AgeRestrictedGuild(c.client, guild)))
	rt := rankText(standings, of)
	meters := []string{
		meter("caught", frac(s.Heard, top), plural(s.Heard, "time", "times")),
		meter("you ask", frac(s.Triggered, top), plural(s.Triggered, "time", "times")),
		meter("fled", frac(s.Fled, top), plural(s.Fled, "time", "times")),
	}
	em := info(title, rt+userBody(meters, w, hours, zone, global)).
		WithTimestamp(time.Now()).
		AddField("Last caught", last, false).
		AddField("Collected", fmt.Sprintf("%d/%d · %d/%d rare %s", col.Got, col.Total, col.GotRare, col.TotalRare, sounds.MarkRare), false)
	return ranked(em, rt), false, nil
}

// userBody draws a person's month under their meters. Here gets the whole month drawn; everywhere
// gets it as one line, because the two go out together and the month that matters is the one in
// the room you asked from.
func userBody(meters []string, w calendar, hours []store.UserHour, zone string, global bool) string {
	caught := daily(w, hours, userAt, heardOf)
	if global {
		return block(append(meters, "", fmt.Sprintf(" %-*s %s", labelWidth, "30 days", spark(caught)))...) + "\n" + streakLine(caught)
	}
	return block(meters...) + "\n" +
		titled("caught a day", columns(caught, w.label(0), w.label(w.n-1))...) +
		titled("when you get caught · "+zone, heatmap(weekHours(w, hours, userAt, heardOf))...) +
		streakLine(caught)
}

// streakLine says how many days running the person has been caught, from two on: one day is not a
// run of anything.
func streakLine(caught []int) string {
	if n := streak(caught); n > 1 {
		return fmt.Sprintf("Caught %d days running.", n)
	}
	return ""
}

// rankUser ranks against a guild's people exactly on read, and against everyone through the
// cut-points.
func (c *Commands) rankUser(ctx context.Context, guild, user snowflake.ID, global bool) ([]standing, error) {
	since := time.Now().Add(-ranks.Window)
	if global {
		n, err := c.events.UserRecent(ctx, user, since)
		return userStandings(n, c.ranks.Below), err
	}
	r, err := c.events.UserRank(ctx, guild, user, since)
	return userStandings(r.UserCounts, exactRank(r)), err
}

// ranked names the window in the footer, since the numbers beside the ranks are all-time.
func ranked(em discord.Embed, rt string) discord.Embed {
	if rt == "" {
		return em
	}
	return em.WithFooterText("ranks: last 30 days")
}

func (c *Commands) statsGuild(ctx context.Context, e *events.ApplicationCommandInteractionCreate, guild snowflake.ID) error {
	s, err := c.events.GuildStats(ctx, guild)
	if err != nil {
		return c.failed(e, "Couldn't count", err)
	}
	if s.PlaysAll == 0 {
		return c.edit(e, none("Nothing played here yet", "Give it five minutes and somebody will regret it."))
	}
	loc, zone := c.zone(guild)
	w := lastDays(time.Now(), loc, trendDays)
	var recent store.GuildRecent
	var hours []store.PlayHour
	var who []store.Row
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() (err error) {
		recent, err = c.events.GuildRecent(gctx, guild, time.Now().Add(-ranks.Window))
		return err
	})
	g.Go(func() (err error) { hours, err = c.events.PlaysHourly(gctx, &guild, w.start); return err })
	g.Go(func() (err error) { who, err = c.events.CharacterPlays(gctx, &guild); return err })
	if err := g.Wait(); err != nil {
		return c.failed(e, "Couldn't count", err)
	}
	rt := rankText(guildStandings(recent, c.ranks.Below), "servers")
	// The peak comes from the month, in the server's own hours; a server quiet for a month falls
	// back to its all-time loudest hour, which only the database knows and only in UTC.
	hour := noneYet
	if h := peakHour(weekHours(w, hours, hourAt, playsOf)); h >= 0 {
		hour = fmt.Sprintf("%02d:00 %s", h, zone)
	} else if s.LoudestHour != nil {
		hour = fmt.Sprintf("%02d:00 UTC", *s.LoudestHour)
	}
	avg := noneYet
	if s.AvgListeners != nil {
		avg = fmt.Sprintf("%.1f", *s.AvgListeners)
	}
	day := noneYet
	plays := daily(w, hours, hourAt, playsOf)
	if i := busiest(plays); i >= 0 {
		day = fmt.Sprintf("%s · %s", w.label(i), plural(plays[i], "play", "plays"))
	}
	em := info("Guild stats", rt+playsReport(w, hours, zone)).
		WithTimestamp(time.Now()).
		AddField("All time", plural(s.PlaysAll, "play", "plays"), true).
		AddField("Avg listeners", avg, true).
		AddField("Fail rate (7d)", pct(s.FailRate7d), true).
		AddField("Top sound", c.orNone(&guild, s.TopSound), true).
		AddField("Peak hour", hour, true).
		AddField("Busiest day", day, true)
	return c.edit(e, fit(ranked(withByCharacter(em, c.byCharacter(who)), rt))...)
}

// zone is the location a server's reports are drawn in, and its name for the caption. An unknown
// zone name is drawn in UTC and says so, rather than failing a report over a clock.
func (c *Commands) zone(guild snowflake.ID) (*time.Location, string) {
	tz := settings.Zone(c.settings.Get(guild))
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return time.UTC, "UTC"
	}
	return loc, tz
}

var medals = []string{"🥇", "🥈", "🥉"}

func rank(i int) string {
	if i < len(medals) {
		return medals[i]
	}
	return fmt.Sprintf("%d.", i+1)
}

func (c *Commands) cmdLeaderboard(ctx context.Context, e *events.ApplicationCommandInteractionCreate, guild snowflake.ID, data discord.SlashCommandInteractionData) error {
	board := data.String("board")
	period, _ := data.OptString("period")
	days, span := window(period)
	public, _ := data.OptBool("public")

	if board == boardGuilds {
		return c.cmdTopGuilds(ctx, e, boardTitles[board], span, days)
	}

	if err := e.DeferCreateMessage(!public); err != nil {
		return err
	}
	rows, err := c.events.Leaderboard(ctx, guild, board, days)
	if err != nil {
		return c.failed(e, "Couldn't rank", err)
	}
	if len(rows) == 0 {
		return c.edit(e, none(boardTitles[board], "Nothing yet. Give it time.").WithFooterText(span))
	}
	foot := span
	if public {
		// Footers do not parse mentions, so naming the requester here cannot ping them.
		foot += " · requested by " + e.User().EffectiveName()
	}
	return c.edit(e, boardEmbed(boardTitles[board], foot, rows, func(r store.Row) string {
		return c.label(guild, board, r.Key)
	}))
}

// window turns the period choice into a day count (0 = all time) and the heading that describes it.
func window(period string) (days int, span string) {
	switch period {
	case "7":
		return 7, "last 7 days"
	case periodAll:
		return 0, "all time"
	}
	return 30, "last 30 days"
}

// label renders one leaderboard key for its board: a sound name, a channel link, or a user mention.
// A sound is clipped here rather than by boardEmbed, which would cut its markers off first.
func (c *Commands) label(guild snowflake.ID, board, key string) string {
	switch board {
	case boardSounds:
		return c.soundIcon(&guild, key) + clipped(key, c.chars.Marks)
	case boardChannels:
		return "<#" + key + ">"
	}
	return "<@" + key + ">"
}

// cmdTopGuilds is the owner-only servers board: guild ids resolved to names from the cache.
func (c *Commands) cmdTopGuilds(ctx context.Context, e *events.ApplicationCommandInteractionCreate, title, span string, days int) error {
	if !c.isOwner(e.User().ID) {
		return e.CreateMessage(say(bad("Not for you", "Owner only.")))
	}
	if err := e.DeferCreateMessage(true); err != nil {
		return err
	}
	rows, err := c.events.TopGuilds(ctx, days)
	if err != nil {
		return c.failed(e, "Couldn't rank", err)
	}
	if len(rows) == 0 {
		return c.edit(e, none(title, "Nothing yet. Give it time.").WithFooterText(span))
	}
	return c.edit(e, boardEmbed(title, span, rows, func(r store.Row) string { return truncate(c.guildName(r.Key), 60) }))
}

// guildName resolves a guild id to its cached name, falling back to the raw id.
func (c *Commands) guildName(key string) string {
	id, err := snowflake.Parse(key)
	if err != nil {
		return key
	}
	g, ok := c.client.Caches.Guild(id)
	if !ok {
		return key
	}
	return g.Name
}

// board renders every leaderboard: one description list, never fields. A rank label is as wide as
// whoever owns it — a mention, a filename, a server name — and a field grid only survives a phone
// when every cell is short.
func boardEmbed(title, footer string, rows []store.Row, label func(store.Row) string) discord.Embed {
	// Rows arrive ranked, so the first is the scale everything else is drawn against.
	top := 0
	for _, r := range rows {
		top = max(top, r.N)
	}
	width := len(strconv.Itoa(top))
	var sb strings.Builder
	for i, r := range rows {
		sb.WriteString(boardRow(i, frac(r.N, top), r.N, width, label(r)) + "\n")
	}
	return embed(colBoard, title, sb.String()).WithFooterText(footer).WithTimestamp(time.Now())
}

// truncate keeps one long name from pushing the description past Discord's 4096, which would have
// the whole board rejected rather than clipped. It counts runes, not bytes: a byte cut splits a
// character on any non-ASCII name, and server names are routinely emoji. Grapheme clusters would
// need x/text and buy nothing but a tidier split of a ZWJ sequence.
func truncate(s string, n int) string {
	i := 0
	for off := range s {
		if i == n {
			return s[:off] + "…"
		}
		i++
	}
	return s
}

// edit replaces the deferred "thinking…" with the answer. Mentions render and nobody is pinged: a
// public board would otherwise notify ten people who did not ask to be ranked.
func (c *Commands) edit(e *events.ApplicationCommandInteractionCreate, em ...discord.Embed) error {
	_, err := c.client.Rest.UpdateInteractionResponse(c.client.ApplicationID, e.Token(), discord.MessageUpdate{
		Embeds:          &em,
		AllowedMentions: &discord.AllowedMentions{},
	}, rest.WithCtx(context.Background()))
	return err
}

// saveThenSay acknowledges before it writes: a write stalled on the pool would otherwise outrun
// Discord's 3s window and show "did not respond" for a change that then lands anyway.
func (c *Commands) saveThenSay(e *events.ApplicationCommandInteractionCreate, write func() error, reply discord.Embed) error {
	if err := e.DeferCreateMessage(true); err != nil {
		return err
	}
	if err := write(); err != nil {
		return c.failed(e, "Couldn't save", err)
	}
	return c.edit(e, reply)
}

// failed answers an interaction that is already deferred, then hands the error back for the log and
// the span. Without it a database failure leaves the user on "thinking…" until Discord gives up.
func (c *Commands) failed(e *events.ApplicationCommandInteractionCreate, title string, err error) error {
	if rerr := c.edit(e, bad(title, "The database isn't answering. Try again in a minute.")); rerr != nil {
		slog.Error("reporting a failed command", slog.Any("err", rerr))
	}
	return err
}
