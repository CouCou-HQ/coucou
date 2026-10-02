package commands

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/cache"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"
)

const (
	testGuild = snowflake.ID(1)
	testSelf  = snowflake.ID(2)
	testUser  = snowflake.ID(3)
	testVoice = snowflake.ID(11)
	testStage = snowflake.ID(12)
	testPerms = discord.PermissionViewChannel | discord.PermissionConnect | discord.PermissionSpeak
)

// channelOfType builds a cached channel. The concrete channel types keep their fields unexported
// and are only ever built by unmarshalling, so the test does the same rather than reaching past that.
func channelOfType(t *testing.T, id snowflake.ID, typ discord.ChannelType) discord.GuildChannel {
	t.Helper()
	raw := fmt.Sprintf(`{"id":"%s","guild_id":"%s","type":%d,"name":"ch%s"}`, id, testGuild, typ, id)
	switch typ {
	case discord.ChannelTypeGuildStageVoice:
		var ch discord.GuildStageVoiceChannel
		if err := json.Unmarshal([]byte(raw), &ch); err != nil {
			t.Fatalf("building stage channel %s: %v", id, err)
		}
		return ch
	default:
		var ch discord.GuildVoiceChannel
		if err := json.Unmarshal([]byte(raw), &ch); err != nil {
			t.Fatalf("building voice channel %s: %v", id, err)
		}
		return ch
	}
}

// testCommands wires a Commands over a real cache: one guild, an @everyone role granting what
// voice.Usable needs, the bot carrying it, and the user sitting in channel.
func testCommands(t *testing.T, channel snowflake.ID, typ discord.ChannelType) *Commands {
	t.Helper()
	c := cache.New(cache.WithCaches(cache.FlagsAll))
	c.SetSelfUser(discord.OAuth2User{User: discord.User{ID: testSelf, Bot: true}})
	c.AddGuild(discord.Guild{ID: testGuild})
	// The @everyone role shares the guild's id, which keeps the test free of channel overwrites.
	c.AddRole(discord.Role{ID: testGuild, GuildID: testGuild, Permissions: testPerms})
	c.AddMember(discord.Member{GuildID: testGuild, User: discord.User{ID: testSelf, Bot: true}, RoleIDs: []snowflake.ID{testGuild}})

	if channel != 0 {
		c.AddChannel(channelOfType(t, channel, typ))
		ch := channel
		c.AddVoiceState(discord.VoiceState{GuildID: testGuild, UserID: testUser, ChannelID: &ch})
		c.AddMember(discord.Member{GuildID: testGuild, User: discord.User{ID: testUser}})
	}
	return &Commands{client: &bot.Client{Caches: c, ApplicationID: testSelf}}
}

// A stage channel is refused for what it is, not for permissions the guild would go looking for.
func TestPlayChannelNamesStageChannelsAsTheReason(t *testing.T) {
	c := testCommands(t, testStage, discord.ChannelTypeGuildStageVoice)

	got, refusal := c.playChannel(testGuild, testUser)
	if refusal == nil {
		t.Fatalf("playChannel allowed stage channel %s", got)
	}
	if refusal.Title != "Stage channels aren't supported" {
		t.Errorf("refusal title = %q, want the stage reason rather than a permission one", refusal.Title)
	}
}

func TestPlayChannelAllowsAVoiceChannel(t *testing.T) {
	c := testCommands(t, testVoice, discord.ChannelTypeGuildVoice)

	got, refusal := c.playChannel(testGuild, testUser)
	if refusal != nil {
		t.Fatalf("playChannel refused a usable channel: %q", refusal.Title)
	}
	if got != testVoice {
		t.Errorf("playChannel = %s, want %s", got, testVoice)
	}
}

func TestPlayChannelRefusesAUserOutOfVoice(t *testing.T) {
	c := testCommands(t, 0, 0)

	if _, refusal := c.playChannel(testGuild, testUser); refusal == nil {
		t.Fatal("playChannel allowed a user who is not in voice")
	} else if refusal.Title != "Not in a voice channel" {
		t.Errorf("refusal title = %q, want the not-in-voice reason", refusal.Title)
	}
}

// A room where everyone is deafened is refused for that, not for permissions the bot has.
func TestPlayChannelRefusesARoomNobodyCanHear(t *testing.T) {
	c := testCommands(t, testVoice, discord.ChannelTypeGuildVoice)
	ch := testVoice
	c.client.Caches.AddVoiceState(discord.VoiceState{GuildID: testGuild, UserID: testUser, ChannelID: &ch, SelfDeaf: true})

	got, refusal := c.playChannel(testGuild, testUser)
	if refusal == nil {
		t.Fatalf("playChannel allowed %s where nobody can hear", got)
	}
	if refusal.Title != "Nobody can hear it" {
		t.Errorf("refusal title = %q, want the deafened reason", refusal.Title)
	}
}
