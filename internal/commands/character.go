package commands

import (
	"cmp"
	"context"
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/omit"
	"github.com/disgoorg/snowflake/v2"

	"github.com/be-sandaa/coucou/internal/characters"
	"github.com/be-sandaa/coucou/internal/profile"
	"github.com/be-sandaa/coucou/internal/settings"
	"github.com/be-sandaa/coucou/internal/store"
	"github.com/be-sandaa/coucou/internal/voice"
)

const (
	cmdNameCharacter = "character"
	subShow          = "show"
	subPreview       = "preview"
	subSwitch        = "switch"
	optCharacter     = "character"

	// switchCooldown keeps a guild well inside Discord's two avatar changes per ten minutes, and
	// keeps the audit log from filling with an admin flicking through characters.
	switchCooldown = time.Hour
)

// characterDefinition is /character, offered only to a bot with more than one character. Each
// character is a choice, so nobody types an id.
func characterDefinition(chars []*characters.Character) discord.SlashCommandCreate {
	// ponytail: choices stop at 25 characters; past that this wants autocomplete.
	choices := make([]discord.ApplicationCommandOptionChoiceString, 0, min(len(chars), maxChoices))
	for _, ch := range chars[:min(len(chars), maxChoices)] {
		choices = append(choices, discord.ApplicationCommandOptionChoiceString{Name: withEmoji(ch.Emoji, ch.Name()), Value: ch.ID})
	}
	pick := []discord.ApplicationCommandOption{
		discord.ApplicationCommandOptionString{Name: optCharacter, Description: descWhich, Required: true, Choices: choices},
	}
	return discord.SlashCommandCreate{
		Name: cmdNameCharacter, Description: "Who the bot is in this server",
		DefaultMemberPermissions: omit.NewPtr(manageGuild),
		Options: []discord.ApplicationCommandOption{
			discord.ApplicationCommandOptionSubCommand{Name: subShow, Description: "The character now, and the others"},
			discord.ApplicationCommandOptionSubCommand{Name: subPreview, Description: fmt.Sprintf("Hear up to %d of a character's sounds in your voice channel", profile.MaxPreview), Options: pick},
			discord.ApplicationCommandOptionSubCommand{Name: subSwitch, Description: "Change who the bot is here: sounds, nickname and avatar", Options: pick},
		},
	}
}

// Definitions is every command this bot registers: /character only when there is someone to switch to.
func (c *Commands) Definitions() []discord.ApplicationCommandCreate {
	if all := c.chars.All(); len(all) > 1 {
		return append(slices.Clone(definitions), characterDefinition(all))
	}
	return definitions
}

func (c *Commands) cmdCharacter(ctx context.Context, e *events.ApplicationCommandInteractionCreate, guild snowflake.ID, data discord.SlashCommandInteractionData) error {
	var sub string
	if data.SubCommandName != nil {
		sub = *data.SubCommandName
	}
	ch := c.chars.Get(data.String(optCharacter))
	switch sub {
	case subPreview:
		return c.previewCharacter(ctx, e, guild, ch)
	case subSwitch:
		return c.switchCharacter(ctx, e, guild, ch)
	default:
		return e.CreateMessage(say(c.characterList(guild)))
	}
}

// characterList is /character show: who the bot is here, then everyone it could be.
func (c *Commands) characterList(guild snowflake.ID) discord.Embed {
	now := c.character(guild)
	var sb strings.Builder
	for _, ch := range c.chars.All() {
		line := "**" + markdown.Replace(withEmoji(ch.Emoji, ch.Name())) + "**"
		if ch.Tagline != "" {
			line += " — " + ch.Tagline
		}
		if ch.ID == now.ID {
			line += " · *now*"
		}
		sb.WriteString(line + "\n")
	}
	sb.WriteString("\n`/character preview` to hear one, `/character switch` to change.")
	return info("Characters", sb.String())
}

func (c *Commands) previewCharacter(ctx context.Context, e *events.ApplicationCommandInteractionCreate, guild snowflake.ID, ch *characters.Character) error {
	channel, refusal := c.playChannel(guild, e.User().ID)
	if refusal != nil {
		return e.CreateMessage(say(*refusal))
	}
	nsfw := c.settings.Get(guild).NSFW.Allows(voice.AgeRestricted(c.client, guild, channel))
	sounds := previewSounds(ch.Preview, ch.Sounds.Names(nsfw), rand.Perm)
	switch {
	case len(sounds) == 0:
		return e.CreateMessage(say(bad("Nothing to preview", markdown.Replace(ch.Name())+" has no sounds that can play here.")))
	case voice.Busy(guild):
		return e.CreateMessage(say(bad("Already busy here", "Let the current one finish.")))
	}
	if next, ok := c.plays.take(guild, e.User().ID); !ok {
		return e.CreateMessage(say(bad("Slow down", fmt.Sprintf("The next play is allowed <t:%d:R>.", next.Unix()))))
	}
	labels := make([]string, len(sounds))
	for i, s := range sounds {
		labels[i] = "`" + ch.Sounds.Label(s) + "`"
	}
	body := strings.Join(labels, " · ") + "\n\nA preview: it does not count in the stats, and nothing here changes." + ownBot(ch)
	reply := info("Previewing "+withEmoji(ch.Emoji, ch.Name()), body)
	if ch.Tagline != "" {
		reply = reply.WithFooterText(ch.Tagline)
	}
	if err := e.CreateMessage(say(reply)); err != nil {
		return err
	}
	c.play(ctx, PlayArgs{Guild: guild, Channel: channel, User: e.User().ID, Character: ch.ID, Preview: sounds})
	return nil
}

// previewSounds is the profile's own picks that can play here, topped up at random from the rest
// to profile.MaxPreview.
func previewSounds(picked, playable []string, perm func(int) []int) []string {
	var out []string
	for _, s := range picked {
		if slices.Contains(playable, s) && !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	for _, i := range perm(len(playable)) {
		if len(out) == profile.MaxPreview {
			break
		}
		if !slices.Contains(out, playable[i]) {
			out = append(out, playable[i])
		}
	}
	return out
}

func (c *Commands) switchCharacter(ctx context.Context, e *events.ApplicationCommandInteractionCreate, guild snowflake.ID, ch *characters.Character) error {
	name := withEmoji(ch.Emoji, ch.Name())
	if c.character(guild).ID == ch.ID {
		return e.CreateMessage(say(info("Already "+name, "Nothing to change.")))
	}
	if next, ok := c.switches.take(guild, time.Now()); !ok {
		return e.CreateMessage(say(bad("Not yet", fmt.Sprintf("This server can switch again <t:%d:R>.", next.Unix()))))
	}
	body := "The sounds change now; the nickname and avatar within a minute. " +
		"A nickname your admins set stays as it is." + ownBot(ch)
	return c.saveThenSay(e, func() error {
		if _, err := c.settings.Update(ctx, guild, e.User().ID, func(s *settings.Settings) { s.Character = ch.ID }); err != nil {
			return err
		}
		c.push(guild)
		return nil
	}, info("Now "+name, body))
}

// ownBot points at the character's own bot, for a server that wants it alongside this one rather
// than instead of whoever this one is.
func ownBot(ch *characters.Character) string {
	if ch.App == 0 {
		return ""
	}
	return "\n\nWant " + markdown.Replace(ch.Name()) + " here at the same time as another character? [Add its own bot](" + inviteURL(ch.App) + ")."
}

// guildCooldown is one wait per guild. The zero value is ready.
type guildCooldown struct {
	mu    sync.Mutex
	until map[snowflake.ID]time.Time
}

// take starts guild's wait and reports true, or reports when the running one ends.
func (g *guildCooldown) take(guild snowflake.ID, now time.Time) (time.Time, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if until := g.until[guild]; now.Before(until) {
		return until, false
	}
	if g.until == nil {
		g.until = map[snowflake.ID]time.Time{}
	}
	g.until[guild] = now.Add(switchCooldown)
	return time.Time{}, true
}

// byCharacter is the stats' "By character": each character's plays, most first, with plays from
// before characters counted as the default's. Empty while only one character has played, which a
// one-character bot always is.
func (c *Commands) byCharacter(rows []store.Row) string {
	counts := map[string]int{}
	var ids []string
	for _, r := range rows {
		id := cmp.Or(r.Key, c.chars.Default().ID)
		if _, ok := counts[id]; !ok {
			ids = append(ids, id)
		}
		counts[id] += r.N
	}
	if len(ids) < 2 {
		return ""
	}
	slices.SortStableFunc(ids, func(a, b string) int { return counts[b] - counts[a] })
	lines := make([]string, len(ids))
	for i, id := range ids {
		name := id // a character since removed is still counted, under its id
		if j := slices.IndexFunc(c.chars.All(), func(ch *characters.Character) bool { return ch.ID == id }); j >= 0 {
			name = withEmoji(c.chars.All()[j].Emoji, c.chars.All()[j].Name())
		}
		lines[i] = markdown.Replace(name) + " · " + plural(counts[id], "play", "plays")
	}
	return strings.Join(lines, "\n")
}

// withByCharacter adds the breakdown to a stats embed when there is one to show.
func withByCharacter(em discord.Embed, by string) discord.Embed {
	if by == "" {
		return em
	}
	return em.AddField("By character", by, false)
}
