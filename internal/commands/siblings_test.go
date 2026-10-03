package commands

import (
	"fmt"
	"strings"
	"testing"

	"github.com/disgoorg/snowflake/v2"
)

const narrator = "The Narrator"

// The bot answering is Honk; The Narrator is the one other bot it can point at.
var (
	siblingSelf, siblingOther = snowflake.ID(1), snowflake.ID(2)
	siblingFixtures           = []Sibling{{Name: "Honk", App: siblingSelf}, {Name: narrator, App: siblingOther}}
)

func noAvatar(snowflake.ID) string { return "" }

// /help has a card for every sibling but the bot answering, and none when that leaves nobody.
func TestFriendCardsLeaveOutItself(t *testing.T) {
	self, other, siblings := siblingSelf, siblingOther, siblingFixtures
	avatar := func(id snowflake.ID) string { return "https://cdn/" + id.String() }

	got := friendCards(siblings, self, "Honk", avatar)
	if len(got) != 1 {
		t.Fatalf("friendCards = %d cards, want only The Narrator's", len(got))
	}
	a := got[0].Author
	if a == nil || a.Name != narrator || a.URL != inviteURL(other) || a.IconURL != avatar(other) {
		t.Errorf("author = %+v, want The Narrator, its invite and its avatar", a)
	}
	if !strings.Contains(got[0].Description, "[Add The Narrator to a server]("+inviteURL(other)+")") {
		t.Errorf("description = %q, want the invite as link text", got[0].Description)
	}
	if got := friendCards(siblings[:1], self, "Honk", avatar); len(got) != 0 {
		t.Errorf("friendCards with only itself = %d cards, want none", len(got))
	}
	if got := friendCards(nil, self, "Honk", avatar); len(got) != 0 {
		t.Errorf("friendCards(nil) = %d cards, want none", len(got))
	}
}

// Past maxFriendCards a friend still gets its invite, as a link on the last card.
func TestFriendCardsOverflowOntoTheLast(t *testing.T) {
	siblings := make([]Sibling, 0, maxFriendCards+2)
	for i := range maxFriendCards + 2 {
		siblings = append(siblings, Sibling{Name: fmt.Sprintf("Bot%d", i), App: snowflake.ID(100 + i)})
	}
	got := friendCards(siblings, siblingSelf, "Honk", noAvatar)
	if len(got) != maxFriendCards {
		t.Fatalf("friendCards = %d cards, want %d", len(got), maxFriendCards)
	}
	last := got[len(got)-1].Description
	for _, s := range siblings[maxFriendCards:] {
		if !strings.Contains(last, "["+s.Name+"]("+inviteURL(s.App)+")") {
			t.Errorf("last card %q does not link %s", last, s.Name)
		}
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

// The name is a nickname any server admin can set, so it is escaped before it lands in the card,
// where a stray ] or * would break the markdown around it.
func TestFriendCardsEscapeTheName(t *testing.T) {
	got := friendCards(siblingFixtures, siblingSelf, "*Mo]an_", noAvatar)
	if !strings.Contains(got[0].Description, `Friend of \*Mo\]an\_.`) {
		t.Errorf("description = %q, want the name escaped", got[0].Description)
	}
}
