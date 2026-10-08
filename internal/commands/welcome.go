package commands

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/snowflake/v2"

	"github.com/be-sandaa/coucou/internal/characters"
	"github.com/be-sandaa/coucou/internal/profile"
	"github.com/be-sandaa/coucou/internal/settings"
)

// welcomeApply opens the Apply button's custom id; the suggestion follows it, so the button still
// works after a restart: welcome:apply:<character>:<chance>.
const welcomeApply = "welcome:apply:"

// welcomeNeeds is what the bot needs in a channel to post its introduction there.
const welcomeNeeds = discord.PermissionViewChannel | discord.PermissionSendMessages | discord.PermissionEmbedLinks

// suggestion is what the bot proposes for a server it just joined.
type suggestion struct {
	Character string
	Chance    int
}

// Server sizes the chance suggestion steps at. ponytail: a guess from member count alone; voice
// activity would say more, and the bot has none of it on the day it joins.
const (
	smallServer = 15
	bigServer   = 500
)

// suggest is the character the server's own words point at and a chance sized to the server.
func suggest(chars []*characters.Character, def *characters.Character, text string, members int) suggestion {
	ch := match(chars, def, strings.ToLower(text))
	return suggestion{Character: ch.ID, Chance: sized(ch.Defaults.Chance, members)}
}

// match is the character whose keywords turn up most in text, the default on a tie or no match,
// and the first character when there is no default to fall back on.
func match(chars []*characters.Character, def *characters.Character, text string) *characters.Character {
	if def.ID == "" {
		def = chars[0]
	}
	best, top, tied := def, 0, false
	for _, ch := range chars {
		n := 0
		for _, k := range ch.Keywords {
			if strings.Contains(text, k) {
				n++
			}
		}
		switch {
		case n > top:
			best, top, tied = ch, n, false
		case n == top && n > 0:
			tied = true
		}
	}
	if tied {
		return def
	}
	return best
}

// sized is a character's default chance for a server of members: more where a visit is rare
// among few people, less where it would be constant among many.
func sized(chance, members int) int {
	switch {
	case members <= smallServer:
		return min(chance*2, profile.MaxChance)
	case members >= bigServer && chance > 1:
		return chance / 2
	}
	return chance
}

// guildText is what a server says about itself on joining: its name, description and channel names.
func guildText(g *discord.GatewayGuild) string {
	parts := []string{g.Name}
	if g.Description != nil {
		parts = append(parts, *g.Description)
	}
	for _, ch := range g.Channels {
		parts = append(parts, ch.Name())
	}
	return strings.Join(parts, " ")
}

// Welcome introduces the bot in a server it just joined, with a suggestion an admin can apply in
// one press. No channel it can post in means no introduction, not an error: the commands still work.
func (c *Commands) Welcome(_ context.Context, g *discord.GatewayGuild) error {
	channel, ok := c.welcomeChannel(g)
	if !ok {
		slog.Info("welcome: no channel to post in", slog.String("guild", g.ID.String()))
		return nil
	}
	s := suggest(c.chars.All(), c.chars.Default(), guildText(g), g.MemberCount)
	_, err := c.client.Rest.CreateMessage(channel, discord.NewMessageCreate().
		WithEmbeds(c.welcomeEmbed(s, 0)).
		AddActionRow(discord.NewPrimaryButton("Use these", welcomeApply+s.Character+":"+strconv.Itoa(s.Chance))))
	if err != nil {
		slog.Warn("welcome: post", slog.String("guild", g.ID.String()), slog.Any("err", err))
	}
	return nil // a retry could post the introduction twice
}

// welcomeChannel is the server's system channel when the bot can post there, else the first text
// channel it can. ponytail: "first" is by position alone, ignoring categories.
func (c *Commands) welcomeChannel(g *discord.GatewayGuild) (snowflake.ID, bool) {
	me, ok := c.client.Caches.SelfMember(g.ID)
	if !ok {
		return 0, false
	}
	can := func(ch discord.GuildChannel) bool {
		return ch.Type() == discord.ChannelTypeGuildText && c.client.Caches.MemberPermissionsInChannel(ch, me).Has(welcomeNeeds)
	}
	if g.SystemChannelID != nil {
		if ch, ok := c.client.Caches.Channel(*g.SystemChannelID); ok && can(ch) {
			return ch.ID(), true
		}
	}
	all := slices.Collect(c.client.Caches.ChannelsForGuild(g.ID))
	slices.SortFunc(all, func(a, b discord.GuildChannel) int { return a.Position() - b.Position() })
	for _, ch := range all {
		if can(ch) {
			return ch.ID(), true
		}
	}
	return 0, false
}

// welcomeEmbed is the introduction: who the bot is, what it does, and the suggestion, or what was
// set from it once by is someone.
func (c *Commands) welcomeEmbed(s suggestion, by snowflake.ID) discord.Embed {
	ch := c.chars.Get(s.Character)
	multi := len(c.chars.All()) > 1
	// Without a default the bot is nobody until someone presses, so it does not claim the suggestion.
	unpicked := by == 0 && c.chars.Default().ID == ""
	var sb strings.Builder
	if ch.Tagline != "" && !unpicked {
		sb.WriteString("*" + ch.Tagline + "*\n\n")
	}
	sb.WriteString("Every 5 minutes there is a chance I drop into a busy voice channel, play something, and leave. " +
		"`/help` has the commands, and the ways to keep me out.\n\n")
	if by == 0 {
		sb.WriteString("**Suggested for this server**\n")
	} else {
		sb.WriteString("**Set for this server** by <@" + by.String() + ">\n")
	}
	if multi {
		sb.WriteString("• Character: **" + markdown.Replace(withEmoji(ch.Emoji, ch.Name())) + "**\n")
	}
	fmt.Fprintf(&sb, "• Chance: **%d%%** every 5 minutes", s.Chance)
	if by == 0 {
		sb.WriteString("\n\nSomeone with Manage Server can use these, or set them later with `/chance`")
		if multi {
			sb.WriteString(" and `/character`")
		}
		sb.WriteString(".")
		if unpicked {
			sb.WriteString(" Until a character is picked, I stay quiet.")
		}
	}
	if unpicked {
		return info("Hi! Who should I be here?", sb.String())
	}
	return info(withEmoji(ch.Emoji, "Hi, I'm "+ch.Name()), sb.String())
}

// applyWelcome saves the suggestion on the button. The message is in a channel, not ephemeral, so
// the press is checked for Manage Server here; the button itself would let anyone in.
func (c *Commands) applyWelcome(e *events.ComponentInteractionCreate) error {
	if e.GuildID() == nil {
		return nil
	}
	guild := *e.GuildID()
	if m := e.Member(); m == nil || !m.Permissions.Has(manageGuild) {
		return e.CreateMessage(say(bad("Not yours to apply", "Someone with Manage Server can.")))
	}
	id, pct, _ := strings.Cut(strings.TrimPrefix(e.Data.CustomID(), welcomeApply), ":")
	chance, err := strconv.Atoi(pct)
	if err != nil || chance < 0 || chance > profile.MaxChance {
		return e.CreateMessage(say(bad("That button is broken", "Set things with `/chance` instead.")))
	}
	if err := e.DeferUpdateMessage(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ch := c.chars.Get(id)
	before := c.character(guild).ID
	d := ch.Defaults
	reply := c.welcomeEmbed(suggestion{Character: ch.ID, Chance: chance}, e.User().ID)
	if _, err = c.settings.Update(ctx, guild, e.User().ID, func(s *settings.Settings) {
		s.Character, s.Chance, s.Suspense, s.FakeOut, s.Encore = ch.ID, chance, d.Suspense, d.FakeOut, d.Encore
	}); err != nil {
		reply = bad("Couldn't save", "Set things with `/chance` instead.")
	} else if ch.ID != before {
		c.push(guild)
	}
	_, uerr := e.Client().Rest.UpdateInteractionResponse(e.ApplicationID(), e.Token(),
		discord.NewMessageUpdate().WithEmbeds(reply).ClearComponents())
	return errors.Join(err, uerr)
}
