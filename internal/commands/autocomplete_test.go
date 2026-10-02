package commands

import (
	"slices"
	"strconv"
	"testing"

	"github.com/disgoorg/disgo/discord"
)

// stringChoices reads the choices back out, failing if anything but a string choice came through.
func stringChoices(t *testing.T, cs []discord.AutocompleteChoice) []discord.AutocompleteChoiceString {
	t.Helper()
	out := make([]discord.AutocompleteChoiceString, 0, len(cs))
	for _, c := range cs {
		s, ok := c.(discord.AutocompleteChoiceString)
		if !ok {
			t.Fatalf("choice %T is not a string choice", c)
		}
		out = append(out, s)
	}
	return out
}

// choiceValues is the sound names a list of choices would play.
func choiceValues(t *testing.T, cs []discord.AutocompleteChoice) []string {
	t.Helper()
	choices := stringChoices(t, cs)
	out := make([]string, 0, len(choices))
	for _, c := range choices {
		out = append(out, c.Value)
	}
	return out
}

const (
	fartLoud  = "fart_loud"
	fartQuiet = "fart_quiet"
	moan      = "moan"
	boom      = "boom"
)

// The query is whatever was typed, against either the file name or what the list shows for it, so
// a match cannot depend on case, or on typing a space where the file has an underscore.
func TestMatchSoundsFoldsCase(t *testing.T) {
	loaded := []string{fartLoud, fartQuiet, moan, boom}

	tests := []struct {
		name string
		q    string
		want []string
	}{
		{"lowercase query", "fart", []string{fartLoud, fartQuiet}},
		{"uppercase query", "BOOM", []string{boom}},
		{"mixed case query", "mOaN", []string{moan}},
		{"the shown name, with a space", "Fart L", []string{fartLoud}},
		{"the file name, with an underscore", "fart_q", []string{fartQuiet}},
		{"empty query offers everything", "", loaded},
		{"no match offers nothing", "zzz", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := choiceValues(t, matchSounds(loaded, tt.q)); !slices.Equal(got, tt.want) {
				t.Errorf("matchSounds(_, %q) = %v, want %v", tt.q, got, tt.want)
			}
		})
	}
}

// The list shows the rendered name and plays the file name: Discord sends back the value, which
// has to resolve in the registry as-is.
func TestMatchSoundsShowsTheRenderedName(t *testing.T) {
	const file, shown = "marta_moan_2", "Marta Moan 2"
	got := stringChoices(t, matchSounds([]string{file}, ""))
	if len(got) != 1 || got[0].Name != shown || got[0].Value != file {
		t.Errorf("matchSounds = %+v, want Name %q Value %q", got, shown, file)
	}
}

func TestMatchSoundsStopsAtDiscordsCap(t *testing.T) {
	loaded := make([]string, maxChoices*2)
	for i := range loaded {
		loaded[i] = "sound" + strconv.Itoa(i)
	}
	if got := matchSounds(loaded, ""); len(got) != maxChoices {
		t.Errorf("got %d choices, want the cap of %d", len(got), maxChoices)
	}
}
