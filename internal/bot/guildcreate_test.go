package bot

import (
	"encoding/json"
	"testing"

	"github.com/disgoorg/disgo"
	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/cache"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/gateway"
	"github.com/disgoorg/snowflake/v2"
)

// A GUILD_CREATE as Discord sends it since Nov 2026: channel type 20 is unknown to disgo, and before
// 0.20 failing to decode it reconnected the shard on an IDENTIFY until Discord reset the token.
const rawGuildCreate = `{
	"id": "1",
	"name": "g",
	"channels": [
		{"id": "20", "type": 20, "name": "new-kind"},
		{"id": "11", "type": 0, "name": "general"}
	],
	"members": [{"user": {"id": "3", "username": "idler", "bot": true}}],
	"voice_states": [{"channel_id": "11", "user_id": "3", "session_id": "s"}]
}`

func guildCreateClient(t *testing.T) *bot.Client {
	t.Helper()
	var c *bot.Client
	// The token's first segment is the base64 application id; nothing here touches the network.
	c, err := disgo.New("MQ.x.y", bot.WithCacheConfigOpts(
		cache.WithCaches(cache.FlagGuilds|cache.FlagMembers|cache.FlagVoiceStates),
		cache.WithMemberCachePolicy(func(m discord.Member) bool {
			_, ok := c.Caches.VoiceState(m.GuildID, m.User.ID)
			return ok
		}),
	))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestGuildCreateHandler(t *testing.T) {
	const guild, idler snowflake.ID = 1, 3
	var e gateway.EventGuildCreate
	if err := json.Unmarshal([]byte(rawGuildCreate), &e); err != nil {
		t.Fatalf("a channel type disgo does not know must not fail the guild: %v", err)
	}

	tests := []struct {
		name   string
		before func(*bot.Client)
		joined bool
	}{
		{"join", func(*bot.Client) {}, true},
		{"boot burst", func(c *bot.Client) { c.Caches.SetGuildUnready(guild, true) }, false},
		{"outage over", func(c *bot.Client) { c.Caches.SetGuildUnavailable(guild, true) }, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := guildCreateClient(t)
			tt.before(c)
			var got *guildJoined
			c.AddEventListeners(bot.NewListenerFunc(func(j *guildJoined) { got = j }))

			gatewayHandlers()[gateway.EventTypeGuildCreate].HandleGatewayEvent(c, 1, 0, e)

			if m, ok := c.Caches.Member(guild, idler); !ok || !m.User.Bot {
				t.Errorf("member = %+v, %v; want the bot in voice cached", m, ok)
			}
			if tt.joined != (got != nil) {
				t.Fatalf("guildJoined dispatched = %v, want %v", got != nil, tt.joined)
			}
			if tt.joined {
				assertFrameKeepsChannels(t, got.Guild)
			}
		})
	}
}

func assertFrameKeepsChannels(t *testing.T, g discord.GatewayGuild) {
	t.Helper()
	raw, err := json.Marshal(g)
	if err != nil {
		t.Fatal(err)
	}
	var back discord.GatewayGuild
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("the frame the bus decodes: %v", err)
	}
	if len(back.Channels) != 2 || back.Channels[1].Name() != "general" {
		t.Errorf("channels = %+v, want both, names intact", back.Channels)
	}
}
