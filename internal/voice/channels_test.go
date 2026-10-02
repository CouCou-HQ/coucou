package voice

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
	guildID = snowflake.ID(1)
	selfID  = snowflake.ID(2)
	afkID   = snowflake.ID(10)
	openID  = snowflake.ID(11)
	stageID = snowflake.ID(12)
)

// voiceChannel builds a cached channel. discord.GuildVoiceChannel keeps its fields unexported and
// is only ever built by unmarshalling, so the test does the same rather than reaching past that.
func voiceChannel(t *testing.T, id snowflake.ID) discord.GuildVoiceChannel {
	t.Helper()
	var ch discord.GuildVoiceChannel
	raw := fmt.Sprintf(`{"id":"%s","guild_id":"%s","type":%d,"name":"ch%s"}`,
		id, guildID, discord.ChannelTypeGuildVoice, id)
	if err := json.Unmarshal([]byte(raw), &ch); err != nil {
		t.Fatalf("building channel %s: %v", id, err)
	}
	return ch
}

// joinVoice puts a user in a channel: a voice state pointing at it plus a member so the bot flag
// is readable, which is exactly the pair the production cache policy keeps.
func joinVoice(c cache.Caches, user, channel snowflake.ID) {
	ch := channel
	c.AddVoiceState(discord.VoiceState{GuildID: guildID, UserID: user, ChannelID: &ch})
	c.AddMember(discord.Member{GuildID: guildID, User: discord.User{ID: user}})
}

// newClient builds a client over a real cache holding one guild, an @everyone role granting the
// permissions Usable needs, and the bot itself carrying that role.
func newClient(t *testing.T, afk *snowflake.ID) *bot.Client {
	t.Helper()
	c := cache.New(cache.WithCaches(cache.FlagsAll))

	// SelfMember resolves through the self-user cache rather than the client's ApplicationID, so
	// without this Usable finds no bot member and refuses every channel.
	c.SetSelfUser(discord.OAuth2User{User: discord.User{ID: selfID, Bot: true}})
	c.AddGuild(discord.Guild{ID: guildID, AfkChannelID: afk})
	// The @everyone role shares the guild's id. Granting on it keeps the test free of per-channel
	// permission overwrites.
	c.AddRole(discord.Role{ID: guildID, GuildID: guildID, Permissions: Needed})
	c.AddMember(discord.Member{GuildID: guildID, User: discord.User{ID: selfID, Bot: true}, RoleIDs: []snowflake.ID{guildID}})

	return &bot.Client{Caches: c, ApplicationID: selfID}
}

// The AFK channel is where Discord parks people who stopped listening, so a crowd in it is the one
// crowd not worth visiting — and everyone in it would otherwise be recorded as a listener.
func TestBestSkipsTheAFKChannel(t *testing.T) {
	afk := afkID
	c := newClient(t, &afk)
	c.Caches.AddChannel(voiceChannel(t, afkID))
	c.Caches.AddChannel(voiceChannel(t, openID))

	// The AFK channel is busier, so picking by headcount alone would choose it.
	joinVoice(c.Caches, 100, afkID)
	joinVoice(c.Caches, 101, afkID)
	joinVoice(c.Caches, 102, openID)

	got, humans := Best(c, guildID, nil)
	if got != openID {
		t.Errorf("Best picked %s, want the non-AFK channel %s", got, openID)
	}
	if len(humans) != 1 {
		t.Errorf("got %d humans, want the 1 in the open channel", len(humans))
	}
}

// With nobody anywhere else, an occupied AFK channel is still not a candidate.
func TestBestReturnsNothingWhenOnlyTheAFKChannelIsPopulated(t *testing.T) {
	afk := afkID
	c := newClient(t, &afk)
	c.Caches.AddChannel(voiceChannel(t, afkID))
	c.Caches.AddChannel(voiceChannel(t, openID))
	joinVoice(c.Caches, 100, afkID)

	if got, humans := Best(c, guildID, nil); len(humans) != 0 {
		t.Errorf("Best picked %s with %d humans, want nothing", got, len(humans))
	}
}

// A guild with no AFK channel configured must not have anything skipped.
func TestBestPicksNormallyWithoutAnAFKChannel(t *testing.T) {
	c := newClient(t, nil)
	c.Caches.AddChannel(voiceChannel(t, openID))
	joinVoice(c.Caches, 100, openID)

	if got, humans := Best(c, guildID, nil); got != openID || len(humans) != 1 {
		t.Errorf("Best = %s with %d humans, want %s with 1", got, len(humans), openID)
	}
}

// Opting out is deliberately partial: it keeps a room that holds only opted-out people from being
// picked, and it does not follow anyone into a room where other people are present. Both halves
// are one test because shipping only the first would be a bot that ignores everyone.
func TestBestIgnoresOptedOutHeadcount(t *testing.T) {
	const optedOut = snowflake.ID(200)

	t.Run("a room of only opted-out people is not worth visiting", func(t *testing.T) {
		c := newClient(t, nil)
		c.Caches.AddChannel(voiceChannel(t, openID))
		joinVoice(c.Caches, optedOut, openID)

		got, humans := Best(c, guildID, func(u snowflake.ID) bool { return u == optedOut })
		if len(humans) != 0 {
			t.Errorf("Best picked %s with %d humans, want nothing", got, len(humans))
		}
	})

	t.Run("they are not counted, but the room is still picked for the others", func(t *testing.T) {
		c := newClient(t, nil)
		c.Caches.AddChannel(voiceChannel(t, openID))
		joinVoice(c.Caches, optedOut, openID)
		joinVoice(c.Caches, 201, openID)

		got, humans := Best(c, guildID, func(u snowflake.ID) bool { return u == optedOut })
		if got != openID {
			t.Errorf("Best picked %s, want %s — somebody else is in there", got, openID)
		}
		if len(humans) != 1 || humans[0] != 201 {
			t.Errorf("humans = %v, want just the person who did not opt out", humans)
		}
	})

	// The filter has to run before the comparison, not after it: picking by raw headcount and
	// filtering the winner would send the bot to the crowded room nobody in it wants it in.
	t.Run("a crowd of opted-out people loses to one person who did not", func(t *testing.T) {
		const alsoOptedOut = snowflake.ID(204)
		c := newClient(t, nil)
		c.Caches.AddChannel(voiceChannel(t, openID))
		c.Caches.AddChannel(voiceChannel(t, afkID)) // no AFK channel configured, so this is an ordinary room
		joinVoice(c.Caches, optedOut, afkID)
		joinVoice(c.Caches, alsoOptedOut, afkID)
		joinVoice(c.Caches, 203, openID)

		ignore := func(u snowflake.ID) bool { return u == optedOut || u == alsoOptedOut }
		if got, humans := Best(c, guildID, ignore); got != openID || len(humans) != 1 {
			t.Errorf("Best = %s with %d humans, want %s with 1 — the busier room counts as empty",
				got, len(humans), openID)
		}
	})
}

// Usable answers what the bot could reach, which is not affected by anyone's preferences — /play
// and /status go through it, and an opt-out must not make them say "can not speak there".
func TestUsableIgnoresOptOuts(t *testing.T) {
	c := newClient(t, nil)
	ch := voiceChannel(t, openID)
	c.Caches.AddChannel(ch)
	joinVoice(c.Caches, 200, openID)

	if humans := Usable(c, guildID, ch); len(humans) != 1 {
		t.Errorf("Usable returned %d humans, want the 1 present regardless of opt-out", len(humans))
	}
}

// stageChannel builds a cached stage channel, the same unmarshalling trick as voiceChannel.
func stageChannel(t *testing.T, id snowflake.ID) discord.GuildStageVoiceChannel {
	t.Helper()
	var ch discord.GuildStageVoiceChannel
	raw := fmt.Sprintf(`{"id":"%s","guild_id":"%s","type":%d,"name":"stage%s"}`,
		id, guildID, discord.ChannelTypeGuildStageVoice, id)
	if err := json.Unmarshal([]byte(raw), &ch); err != nil {
		t.Fatalf("building stage channel %s: %v", id, err)
	}
	return ch
}

// A stage joiner sits in the audience with suppress set, so a busy stage is a room the bot would
// play to in silence while recording the play as ok.
func TestUsableRejectsStageChannels(t *testing.T) {
	c := newClient(t, nil)
	stage := stageChannel(t, stageID)
	c.Caches.AddChannel(stage)
	joinVoice(c.Caches, 100, stageID)

	if humans := Usable(c, guildID, stage); humans != nil {
		t.Errorf("Usable returned %d humans for a stage channel, want nil", len(humans))
	}
}

// And Best must not pick one even when it is the busiest room in the guild.
func TestBestSkipsStageChannels(t *testing.T) {
	c := newClient(t, nil)
	c.Caches.AddChannel(stageChannel(t, stageID))
	c.Caches.AddChannel(voiceChannel(t, openID))

	joinVoice(c.Caches, 100, stageID)
	joinVoice(c.Caches, 101, stageID)
	joinVoice(c.Caches, 102, openID)

	if got, humans := Best(c, guildID, nil); got != openID || len(humans) != 1 {
		t.Errorf("Best = %s with %d humans, want the voice channel %s with 1", got, len(humans), openID)
	}
}

// A deafened person hears nothing, so they are no listener: not in the headcount, not in the
// snapshot, and a room of only them is empty.
func TestHumansSkipsTheDeafened(t *testing.T) {
	tests := []struct {
		name string
		vs   discord.VoiceState
		want int
	}{
		{"listening", discord.VoiceState{}, 1},
		{"self-deafened", discord.VoiceState{SelfDeaf: true}, 0},
		{"server-deafened", discord.VoiceState{GuildDeaf: true}, 0},
		{"muted still hears", discord.VoiceState{SelfMute: true, GuildMute: true}, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newClient(t, nil)
			joinVoice(c.Caches, 100, openID)
			ch := openID
			tt.vs.GuildID, tt.vs.UserID, tt.vs.ChannelID = guildID, 100, &ch
			c.Caches.AddVoiceState(tt.vs)

			if got := Humans(c, guildID, openID); len(got) != tt.want {
				t.Errorf("Humans = %v, want %d", got, tt.want)
			}
		})
	}
}

// Both Discord switches have to be on: an age-restricted server alone, or an age-restricted channel
// in a server that is not, keeps nsfw sounds off.
func TestAgeRestricted(t *testing.T) {
	tests := []struct {
		name  string
		level discord.NSFWLevel
		nsfw  bool
		want  bool
	}{
		{"both", discord.NSFWLevelAgeRestricted, true, true},
		{"explicit server", discord.NSFWLevelExplicit, true, true},
		{"server only", discord.NSFWLevelAgeRestricted, false, false},
		{"channel only, default server", discord.NSFWLevelDefault, true, false},
		{"channel only, safe server", discord.NSFWLevelSafe, true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newClient(t, nil)
			c.Caches.AddGuild(discord.Guild{ID: guildID, NSFWLevel: tt.level})
			var ch discord.GuildVoiceChannel
			raw := fmt.Sprintf(`{"id":"%s","guild_id":"%s","type":%d,"name":"ch","nsfw":%v}`,
				openID, guildID, discord.ChannelTypeGuildVoice, tt.nsfw)
			if err := json.Unmarshal([]byte(raw), &ch); err != nil {
				t.Fatal(err)
			}
			c.Caches.AddChannel(ch)
			if got := AgeRestricted(c, guildID, openID); got != tt.want {
				t.Errorf("AgeRestricted = %v, want %v", got, tt.want)
			}
		})
	}
}
