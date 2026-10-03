package commands

import (
	"strings"
	"testing"

	"github.com/disgoorg/snowflake/v2"
)

// The bot answering is Honk; The Narrator is the one other bot it can point at.
var (
	siblingSelf, siblingOther = snowflake.ID(1), snowflake.ID(2)
	siblingFixtures           = []Sibling{{Name: "Honk", App: siblingSelf}, {Name: "The Narrator", App: siblingOther}}
)

// /help lists every sibling but the bot answering, and says nothing when that leaves nobody.
func TestSiblingsHelpLeavesOutItself(t *testing.T) {
	self, other, siblings := siblingSelf, siblingOther, siblingFixtures

	got := siblingsHelp(siblings, self, "Honk")
	if strings.Contains(got, "[Honk](") || !strings.Contains(got, "[The Narrator]("+inviteURL(other)+")") {
		t.Errorf("siblingsHelp = %q, want only The Narrator's invite", got)
	}
	if got := siblingsHelp(siblings[:1], self, "Honk"); got != "" {
		t.Errorf("siblingsHelp with only itself = %q, want empty", got)
	}
	if got := siblingsHelp(nil, self, "Honk"); got != "" {
		t.Errorf("siblingsHelp(nil) = %q, want empty", got)
	}
}

// The plug shows on the 1-in-playAdOdds roll only, names one sibling that is not this bot, and stays
// away entirely when there is no such sibling.
func TestPlayAd(t *testing.T) {
	self, other, siblings := siblingSelf, siblingOther, siblingFixtures
	always := func(int) int { return 0 }

	tests := []struct {
		name     string
		siblings []Sibling
		intN     func(int) int
		want     string
	}{
		{"most rolls show nothing", siblings, func(int) int { return 1 }, ""},
		{"the lucky roll names a sibling", siblings, always, "[The Narrator](" + inviteURL(other) + ")"},
		{"only itself to plug", siblings[:1], always, ""},
		{"no siblings configured", nil, always, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := playAd(tt.siblings, self, "Honk", tt.intN)
			if (tt.want == "") != (got == "") || !strings.Contains(got, tt.want) {
				t.Errorf("playAd = %q, want one containing %q", got, tt.want)
			}
		})
	}
}

// The name is a nickname any server admin can set, so it is escaped before it lands in bold or in a
// link label, where a stray ] or * would break the markdown around it.
func TestSiblingsHelpEscapesTheName(t *testing.T) {
	got := siblingsHelp(siblingFixtures, siblingSelf, "*Mo]an_")
	if !strings.Contains(got, `**Friends of \*Mo\]an\_**`) {
		t.Errorf("siblingsHelp = %q, want the name escaped inside the heading", got)
	}
}
