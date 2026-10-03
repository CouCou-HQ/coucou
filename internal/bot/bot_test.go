package bot

import (
	"reflect"
	"testing"

	"github.com/disgoorg/disgo/cache"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/gateway"
	"github.com/disgoorg/snowflake/v2"

	"github.com/be-sandaa/coucou/internal/profile"
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

// A custom status carries its text in state, the others in name; no text sends no activity at all.
func TestPresence(t *testing.T) {
	custom, listening := discord.ActivityTypeCustom, discord.ActivityTypeListening
	text := "🖤 lurking"
	tests := []struct {
		name string
		in   profile.Status
		want []discord.Activity
	}{
		{"custom", profile.Status{Text: text, Activity: custom}, []discord.Activity{{Name: "Custom Status", Type: custom, State: &text}}},
		{"listening", profile.Status{Text: text, Activity: listening}, []discord.Activity{{Name: text, Type: listening}}},
		{"no text", profile.Status{Activity: custom}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.in.Online = discord.OnlineStatusIdle
			var got gateway.MessageDataPresenceUpdate
			for _, opt := range presence(tt.in) {
				opt(&got)
			}
			if got.Status != discord.OnlineStatusIdle || !reflect.DeepEqual(got.Activities, tt.want) {
				t.Errorf("presence = %s %+v, want idle %+v", got.Status, got.Activities, tt.want)
			}
		})
	}
}
