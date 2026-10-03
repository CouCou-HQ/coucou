package commands

import (
	"strings"
	"testing"

	"github.com/disgoorg/disgo/discord"

	"github.com/be-sandaa/coucou/internal/sounds"
)

const spin = "spin"

// A sound shows the app emoji named exactly like it; one without keeps today's text, speaker and all.
func TestSoundEmojis(t *testing.T) {
	c := &Commands{sounds: sounds.New(t.TempDir())}
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
			if got := c.orNone(&tt.sound); !strings.HasPrefix(got, tt.icon+"`") {
				t.Errorf("orNone(%s) = %q, want it to open with %q", tt.sound, got, tt.icon)
			}
			if got := c.label(boardSounds, tt.sound); !strings.HasPrefix(got, tt.icon+"`") {
				t.Errorf("label(%s) = %q, want it to open with %q", tt.sound, got, tt.icon)
			}
			// A custom emoji does not render inside a fence, so it replaces the speaker block.
			if got := c.nowPlaying(tt.sound); strings.Contains(got, "```") != (tt.icon == "") {
				t.Errorf("nowPlaying(%s) = %q", tt.sound, got)
			}
		})
	}
}
