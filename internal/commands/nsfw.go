package commands

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"

	"github.com/be-sandaa/coucou/internal/bus"
	"github.com/be-sandaa/coucou/internal/settings"
	"github.com/be-sandaa/coucou/internal/voice"
)

const (
	cmdNameNSFW = "nsfw"
	optMode     = "mode"
	// The custom ids of the buttons under /nsfw on's confirmation.
	nsfwConfirm = "nsfw:on"
	nsfwCancel  = "nsfw:cancel"
)

// nsfwModes says what each mode does, the same way in every reply that names one.
var nsfwModes = map[settings.NSFW]string{
	settings.NSFWOff:        "18+ sounds never play here.",
	settings.NSFWRestricted: "18+ sounds play only where Discord has age-restricted the server or the voice channel.",
	settings.NSFWOn:         "18+ sounds play in every voice channel here, age-restricted or not.",
}

// nsfwOnWarning is asked before on is saved: it moves keeping minors away from adult content from
// Discord's age gate onto the server, and the admin choosing it should say so knowingly.
const nsfwOnWarning = "18+ sounds will play in **every voice channel** here, including ones Discord has not " +
	"age-restricted and anyone can join, minors included.\n\n" +
	"Discord's rules put adult content behind its age gate, so keeping minors away from these sounds " +
	"becomes this server's job, and a server or the bot can be reported for it.\n\n" +
	"Turning it on is recorded with your name and the time."

func (c *Commands) cmdNSFW(ctx context.Context, e *events.ApplicationCommandInteractionCreate, guild snowflake.ID, data discord.SlashCommandInteractionData) error {
	current := c.nsfwMode(guild)
	mode, given := data.OptString(optMode)
	switch next := settings.NSFW(mode); {
	case !given:
		return e.CreateMessage(say(c.nsfwState(guild, current)))
	case next == current:
		return e.CreateMessage(say(info("18+ sounds: "+mode, "Already "+mode+". "+nsfwModes[next])))
	case next == settings.NSFWOn:
		return e.CreateMessage(say(bad("Turn 18+ sounds on everywhere?", nsfwOnWarning)).AddActionRow(
			discord.NewDangerButton("Turn on everywhere", nsfwConfirm),
			discord.NewSecondaryButton("Keep "+string(current), nsfwCancel),
		))
	default:
		return c.saveThenSay(e, func() error { return c.setNSFW(ctx, guild, e.User().ID, next) },
			info("18+ sounds: "+mode, nsfwModes[next]))
	}
}

// nsfwMode is the guild's mode, restricted when it has never chosen one.
func (c *Commands) nsfwMode(guild snowflake.ID) settings.NSFW {
	if m := c.settings.Get(guild).NSFW; m != "" {
		return m
	}
	return settings.NSFWRestricted
}

// nsfwState is /nsfw with no mode: what it is, what that means in this server today, and how to
// change it.
func (c *Commands) nsfwState(guild snowflake.ID, mode settings.NSFW) discord.Embed {
	body := nsfwModes[mode]
	if mode == settings.NSFWRestricted {
		switch {
		case voice.AgeRestrictedServer(c.client, guild):
			body += " Discord has this whole server age-restricted, so that is every voice channel."
		case voice.AgeRestrictedGuild(c.client, guild):
			body += " Some voice channels here are."
		default:
			body += " Nothing here is yet, so none play."
		}
	}
	body += "\n\n`/nsfw mode:` changes it: **off**, **restricted**, or **on** for every voice channel."
	return info("18+ sounds: "+string(mode), body)
}

// setNSFW saves the mode. The settings audit records who and when, as it does for every setting;
// turning it on everywhere is also logged as a warning, so it shows without querying that record.
func (c *Commands) setNSFW(ctx context.Context, guild, by snowflake.ID, mode settings.NSFW) error {
	if _, err := c.settings.Update(ctx, guild, by, func(s *settings.Settings) { s.NSFW = mode }); err != nil {
		return err
	}
	if mode == settings.NSFWOn {
		slog.Warn("nsfw sounds turned on in every channel", slog.Any("guild", guild), slog.Any("by", by))
	}
	c.bus.Publish(ctx, bus.SettingsChanged{Guild: guild, Field: cmdNameNSFW, By: by})
	return nil
}

// OnComponent answers the buttons this package sends: /nsfw on's confirmation and the welcome's Apply.
func (c *Commands) OnComponent() bot.EventListener {
	return bot.NewListenerFunc(func(e *events.ComponentInteractionCreate) { go c.onComponent(e) })
}

func (c *Commands) onComponent(e *events.ComponentInteractionCreate) {
	defer logPanic("component")
	var err error
	switch id := e.Data.CustomID(); {
	case id == nsfwConfirm:
		err = c.confirmNSFW(e)
	case id == nsfwCancel:
		err = e.UpdateMessage(discord.NewMessageUpdate().WithEmbeds(none("Left as it was", "")).ClearComponents())
	case id == forgetConfirm:
		err = c.confirmForget(e)
	case id == forgetCancel:
		err = e.UpdateMessage(discord.NewMessageUpdate().WithEmbeds(none("Nothing erased", "")).ClearComponents())
	case strings.HasPrefix(id, welcomeApply):
		err = c.applyWelcome(e)
	default:
		return
	}
	if err != nil {
		slog.Error("component failed", slog.String("id", e.Data.CustomID()), slog.Any("err", err))
	}
}

// confirmNSFW saves on and replaces the question with the outcome. Who may get this far is the
// command's permission, Manage Server unless the server changed it under Integrations, and the
// question is ephemeral, so only whoever ran /nsfw can press it.
func (c *Commands) confirmNSFW(e *events.ComponentInteractionCreate) error {
	if e.GuildID() == nil {
		return nil
	}
	if err := e.DeferUpdateMessage(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	reply := info("18+ sounds: on", nsfwModes[settings.NSFWOn])
	err := c.setNSFW(ctx, *e.GuildID(), e.User().ID, settings.NSFWOn)
	if err != nil {
		reply = bad("Couldn't save", "The database isn't answering. Try again in a minute.")
	}
	if _, uerr := c.client.Rest.UpdateInteractionResponse(c.client.ApplicationID, e.Token(),
		discord.NewMessageUpdate().WithEmbeds(reply).ClearComponents(), rest.WithCtx(ctx)); uerr != nil {
		slog.Error("answering the nsfw confirmation", slog.Any("err", uerr))
	}
	return err
}
