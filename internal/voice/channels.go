package voice

import (
	"slices"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"
)

// Needed is the permission set a channel has to grant before the bot can do anything in it. It is
// exported because it is also what the invite link asks for: the set a channel is checked against
// and the set the bot is added with are the same fact, and two copies of it drift.
const Needed = discord.PermissionViewChannel | discord.PermissionConnect | discord.PermissionSpeak

// Humans returns the non-bot, non-deafened user ids in a voice channel, from the voice-state cache
// alone: someone who cannot hear the sound is not a listener. The member cache only holds members
// currently in voice (see the cache policy in internal/bot), so the bot flag is there to read.
func Humans(c *bot.Client, guild, channel snowflake.ID) []snowflake.ID {
	var out []snowflake.ID
	for vs := range c.Caches.VoiceStates(guild) {
		if vs.ChannelID == nil || *vs.ChannelID != channel || vs.SelfDeaf || vs.GuildDeaf {
			continue
		}
		if m, ok := c.Caches.Member(guild, vs.UserID); ok && m.User.Bot {
			continue
		}
		if vs.UserID == c.ApplicationID {
			continue
		}
		out = append(out, vs.UserID)
	}
	return out
}

// AgeRestrictedServer reports whether Discord has the whole server age-restricted. EXPLICIT counts
// too: it is the other level that marks a server's content as adult, where DEFAULT and SAFE do not.
func AgeRestrictedServer(c *bot.Client, guild snowflake.ID) bool {
	g, ok := c.Caches.Guild(guild)
	return ok && (g.NSFWLevel == discord.NSFWLevelAgeRestricted || g.NSFWLevel == discord.NSFWLevelExplicit)
}

// AgeRestrictedGuild reports whether the server has anywhere age-restricted for nsfw sounds: the
// whole server, or at least one voice channel.
func AgeRestrictedGuild(c *bot.Client, guild snowflake.ID) bool {
	if AgeRestrictedServer(c, guild) {
		return true
	}
	for ch := range c.Caches.ChannelsForGuild(guild) {
		if ageRestricted(ch) {
			return true
		}
	}
	return false
}

// AgeRestricted reports whether Discord has channel age-restricted: the whole server is, which
// covers every channel in it, or the channel itself carries the label.
func AgeRestricted(c *bot.Client, guild, channel snowflake.ID) bool {
	if AgeRestrictedServer(c, guild) {
		return true
	}
	ch, ok := c.Caches.Channel(channel)
	return ok && ageRestricted(ch)
}

func ageRestricted(ch discord.GuildChannel) bool {
	m, ok := ch.(discord.GuildMessageChannel)
	return ok && m.NSFW()
}

// Usable reports the humans in ch when the bot could join it and there is somebody to join, and
// nil otherwise. The two conditions are one answer because every caller needs both.
//
// Stage channels are not usable. Joining one lands the bot in the audience with suppress set —
// voice.Conn.Open sends only self_mute/self_deaf — and clearing that needs MuteMembers, so frames
// would go out to nobody while the play recorded ok.
func Usable(c *bot.Client, guild snowflake.ID, ch discord.GuildChannel) []snowflake.ID {
	if ch.Type() != discord.ChannelTypeGuildVoice {
		return nil
	}
	humans := Humans(c, guild, ch.ID())
	if len(humans) == 0 {
		return nil
	}
	me, ok := c.Caches.SelfMember(guild)
	if !ok {
		return nil
	}
	if !c.Caches.MemberPermissionsInChannel(ch, me).Has(Needed) {
		return nil
	}
	return humans
}

// Best picks the busiest usable voice channel in a guild, and the humans in it.
//
// ignore drops people from the headcount without hiding the channel: a room holding only ignored
// people has nobody worth visiting and is never picked, but a room where others are present is
// still fair game and the ignored people there still hear it. Only the loop passes one — /play and
// /status ask what is reachable, not what is worth dropping in on, and go through Usable directly.
// nil ignores nobody.
//
// The AFK channel is never picked. Discord parks idle people there, so it is the one channel where
// a full room means nobody is listening — and everyone in it would still be recorded as a listener.
// The check lives here rather than in Usable because it asks whether the bot should drop in, not
// whether it could: an explicit /play from someone sitting in the AFK channel is still a request,
// and refusing that through Usable would answer "can't speak there", which is not the reason.
func Best(c *bot.Client, guild snowflake.ID, ignore func(snowflake.ID) bool) (snowflake.ID, []snowflake.ID) {
	var afk snowflake.ID // zero is no channel, so a guild without one matches nothing
	if g, ok := c.Caches.Guild(guild); ok && g.AfkChannelID != nil {
		afk = *g.AfkChannelID
	}
	var best snowflake.ID
	var bestHumans []snowflake.ID
	for ch := range c.Caches.ChannelsForGuild(guild) {
		if ch.ID() == afk {
			continue
		}
		h := Usable(c, guild, ch)
		if ignore != nil {
			h = slices.DeleteFunc(h, ignore) // Usable builds a fresh slice, so filtering in place surprises no caller
		}
		if len(h) > len(bestHumans) {
			best, bestHumans = ch.ID(), h
		}
	}
	return best, bestHumans
}
