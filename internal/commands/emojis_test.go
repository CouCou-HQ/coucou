package commands

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"

	"github.com/be-sandaa/coucou/internal/characters"
	"github.com/be-sandaa/coucou/internal/profile"
	"github.com/be-sandaa/coucou/internal/settings"
)

const spin = "spin"

// A sound shows the app emoji named exactly like it; one without keeps today's text, speaker and all.
func TestSoundEmojis(t *testing.T) {
	chars, err := characters.New([]profile.Profile{{ID: "x", Dir: t.TempDir()}}, "")
	if err != nil {
		t.Fatal(err)
	}
	c := &Commands{chars: chars}
	c.emojis.set([]discord.Emoji{{ID: 1, Name: boom}, {ID: 2, Name: spin, Animated: true}})

	tests := []struct {
		sound, icon string
	}{
		{boom, "<:boom:1> "},
		{spin, "<a:spin:2> "},
		{"Boom", ""},
		{"zap", ""},
	}
	for _, tt := range tests {
		t.Run(tt.sound, func(t *testing.T) {
			if got := c.emojis.icon(tt.sound); got != tt.icon {
				t.Errorf("icon(%s) = %q, want %q", tt.sound, got, tt.icon)
			}
			if got := c.orNone(nil, &tt.sound); !strings.HasPrefix(got, tt.icon+"`") {
				t.Errorf("orNone(%s) = %q, want it to open with %q", tt.sound, got, tt.icon)
			}
			if got := c.label(0, boardSounds, tt.sound); !strings.HasPrefix(got, tt.icon+"`") {
				t.Errorf("label(%s) = %q, want it to open with %q", tt.sound, got, tt.icon)
			}
			// A custom emoji does not render inside a fence, so it replaces the speaker block.
			if got := c.nowPlaying(0, tt.sound); strings.Contains(got, "```") != (tt.icon == "") {
				t.Errorf("nowPlaying(%s) = %q", tt.sound, got)
			}
		})
	}
}

// With several characters a sound's emoji is <character>_<sound>: the server's character's when it
// has the sound, else the first that does, and a plain-named emoji is not used.
func TestSoundEmojisPerCharacter(t *testing.T) {
	const lisaJr, kick = "lisa-jr", "kick"
	chars, err := characters.New([]profile.Profile{
		{ID: "bart", Dir: soundDir(t, boom, kick)},
		{ID: lisaJr, Dir: soundDir(t, boom)},
	}, lisaJr)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := chars.Start(ctx, time.Hour, func(string) func(string, bool) { return nil }); err != nil {
		t.Fatal(err)
	}
	c := &Commands{chars: chars, settings: settings.New(nil)}
	c.emojis.set([]discord.Emoji{{ID: 1, Name: "bart_boom"}, {ID: 2, Name: "lisa_jr_boom"}, {ID: 3, Name: "bart_kick"}, {ID: 4, Name: boom}})

	g := snowflake.ID(7) // no settings, so the default character, lisa-jr
	tests := []struct {
		name  string
		guild *snowflake.ID
		sound string
		want  string
	}{
		{"the server's character", &g, boom, "<:lisa_jr_boom:2> "},
		{"bot-wide takes the first", nil, boom, "<:bart_boom:1> "},
		{"another character's sound", &g, kick, "<:bart_kick:3> "},
		{"nobody's sound", &g, unknownSound, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := c.soundIcon(tt.guild, tt.sound); got != tt.want {
				t.Errorf("soundIcon(%s) = %q, want %q", tt.sound, got, tt.want)
			}
		})
	}
}

const unknownSound = "zap"

// soundDir is a profile directory whose sounds/ holds an ogg opus file per name.
func soundDir(t *testing.T, names ...string) string {
	t.Helper()
	dir := t.TempDir()
	sd := filepath.Join(dir, "sounds")
	if err := os.Mkdir(sd, 0o755); err != nil {
		t.Fatal(err)
	}
	ogg := append(append(append([]byte("OggS"), make([]byte, 24)...), "OpusHead"...), make([]byte, 24)...)
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(sd, n+".ogg"), ogg, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}
