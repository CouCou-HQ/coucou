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

// /help has a grid field for every sibling but the bot answering, and no grid when that leaves nobody.
func TestFriendsGridLeavesOutItself(t *testing.T) {
	self, other, siblings := siblingSelf, siblingOther, siblingFixtures

	got := friendsGrid(siblings, self, "Honk")
	if len(got) != 1 || len(got[0].Fields) != 1 {
		t.Fatalf("friendsGrid = %+v, want one embed with only The Narrator", got)
	}
	f := got[0].Fields[0]
	if f.Name != narrator || f.Value != "[Add to a server]("+inviteURL(other)+")" || f.Inline == nil || !*f.Inline {
		t.Errorf("field = %+v, want The Narrator's invite, inline", f)
	}
	if got := friendsGrid(siblings[:1], self, "Honk"); got != nil {
		t.Errorf("friendsGrid with only itself = %+v, want nothing", got)
	}
	if got := friendsGrid(nil, self, "Honk"); got != nil {
		t.Errorf("friendsGrid(nil) = %+v, want nothing", got)
	}
}

// Discord rejects an embed past 25 fields, so the grid stops there instead.
func TestFriendsGridStopsAtTheFieldCap(t *testing.T) {
	siblings := make([]Sibling, 0, maxFriends+5)
	for i := range maxFriends + 5 {
		siblings = append(siblings, Sibling{Name: fmt.Sprintf("Bot%d", i), App: snowflake.ID(100 + i)})
	}
	if got := friendsGrid(siblings, siblingSelf, "Honk"); len(got[0].Fields) != maxFriends {
		t.Errorf("friendsGrid = %d fields, want %d", len(got[0].Fields), maxFriends)
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
