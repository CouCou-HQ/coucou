package commands

import (
	"strings"
	"testing"

	"github.com/be-sandaa/coucou/internal/characters"
	"github.com/be-sandaa/coucou/internal/profile"
	"github.com/be-sandaa/coucou/internal/store"
)

func keywordSet(t *testing.T) *characters.Set {
	t.Helper()
	s, err := characters.New([]profile.Profile{
		{ID: bart, Dir: t.TempDir(), Keywords: []string{"skate", "prank"}, Defaults: store.Defaults{Chance: 5}},
		{ID: lisa, Dir: t.TempDir(), Keywords: []string{"jazz", "sax", "book"}, Defaults: store.Defaults{Chance: 4}},
	}, bart)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

const gaming = "Gaming #general"

func TestSuggest(t *testing.T) {
	tests := []struct {
		name, text string
		members    int
		want       suggestion
	}{
		{"keywords pick the character", "Jazz Club #sax-talk #general", 100, suggestion{lisa, 4}},
		{"no keyword is the default", gaming, 100, suggestion{bart, 5}},
		{"a tie is the default", "skate jazz", 100, suggestion{bart, 5}},
		{"most keywords wins", "skate jazz sax", 100, suggestion{lisa, 4}},
		{"small servers get more", gaming, 8, suggestion{bart, 10}},
		{"big servers get less", gaming, 2000, suggestion{bart, 2}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := keywordSet(t)
			if got := suggest(s.All(), s.Default(), tt.text, tt.members); got != tt.want {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

// The suggestion names a character only when there is a choice of them, and offers to be applied
// until someone has.
func TestWelcomeEmbed(t *testing.T) {
	one, err := characters.New([]profile.Profile{{ID: bart, Dir: t.TempDir()}}, "")
	if err != nil {
		t.Fatal(err)
	}
	single := (&Commands{chars: one}).welcomeEmbed(suggestion{bart, 5}, 0).Description
	if strings.Contains(single, "Character:") || strings.Contains(single, "/character") {
		t.Errorf("one character: %q mentions characters", single)
	}
	multi := &Commands{chars: keywordSet(t)}
	if got := multi.welcomeEmbed(suggestion{lisa, 4}, 0).Description; !strings.Contains(got, "Character: **lisa**") || !strings.Contains(got, "Manage Server") {
		t.Errorf("suggested: %q", got)
	}
	if got := multi.welcomeEmbed(suggestion{lisa, 4}, 42).Description; !strings.Contains(got, "by <@42>") || strings.Contains(got, "Manage Server") {
		t.Errorf("applied: %q", got)
	}
}
