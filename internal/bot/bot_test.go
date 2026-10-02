package bot

import (
	"testing"

	"github.com/disgoorg/disgo/cache"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"
)

// Replays disgo's GUILD_CREATE order — members, then voice states — against the in-voice policy.
func TestRecacheMembersKeepsBotsAlreadyInVoice(t *testing.T) {
	const guild, channel, idler snowflake.ID = 1, 2, 3
	var c cache.Caches
	c = cache.New(
		cache.WithCaches(cache.FlagMembers|cache.FlagVoiceStates),
		cache.WithMemberCachePolicy(func(m discord.Member) bool {
			_, ok := c.VoiceState(m.GuildID, m.User.ID)
			return ok
		}),
	)
	ch := channel
	g := discord.GatewayGuild{
		RestGuild:   discord.RestGuild{Guild: discord.Guild{ID: guild}},
		Members:     []discord.Member{{User: discord.User{ID: idler, Bot: true}}},
		VoiceStates: []discord.VoiceState{{GuildID: guild, ChannelID: &ch, UserID: idler}},
	}

	for _, m := range g.Members {
		m.GuildID = guild
		c.AddMember(m)
	}
	for _, vs := range g.VoiceStates {
		c.AddVoiceState(vs)
	}
	if _, ok := c.Member(guild, idler); ok {
		t.Fatal("disgo's order cached the member; the policy no longer depends on it and this test is moot")
	}

	recacheMembers(c, g)
	m, ok := c.Member(guild, idler)
	if !ok || !m.User.Bot {
		t.Fatalf("member after recache = %+v, %v; want the bot cached", m, ok)
	}
}
