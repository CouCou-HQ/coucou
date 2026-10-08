package commands

import (
	"reflect"
	"testing"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"

	"github.com/be-sandaa/coucou/internal/characters"
	"github.com/be-sandaa/coucou/internal/profile"
	"github.com/be-sandaa/coucou/internal/settings"
	"github.com/be-sandaa/coucou/internal/store"
)

const bart, lisa = "bart", "lisa"

func charSet(t *testing.T, ids ...string) *characters.Set {
	t.Helper()
	ps := make([]profile.Profile, len(ids))
	for i, id := range ids {
		ps[i] = profile.Profile{ID: id, Dir: t.TempDir()}
	}
	s, err := characters.New(ps, ids[0])
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// /character exists only where there is someone to switch to.
func TestCharacterIsDeployedOnlyWithTwo(t *testing.T) {
	if one := (&Commands{chars: charSet(t, bart)}).Definitions(); len(one) != len(definitions) {
		t.Errorf("one character: %d commands, want the %d without /character", len(one), len(definitions))
	}
	two := (&Commands{chars: charSet(t, bart, lisa)}).Definitions()
	if len(two) != len(definitions)+1 {
		t.Fatalf("two characters: %d commands, want %d", len(two), len(definitions)+1)
	}
	if cmd, ok := two[len(two)-1].(discord.SlashCommandCreate); !ok || cmd.Name != cmdNameCharacter {
		t.Errorf("last command = %+v, want /character", two[len(two)-1])
	}
	if len(definitions) == len(two) {
		t.Error("Definitions grew the shared slice")
	}
}

// Each character is a choice, so nobody types an id.
func TestCharacterChoices(t *testing.T) {
	cmd := characterDefinition(charSet(t, bart, lisa).All())
	sw, ok := cmd.Options[2].(discord.ApplicationCommandOptionSubCommand)
	if !ok {
		t.Fatalf("third option = %+v, want the switch subcommand", cmd.Options[2])
	}
	opt, ok := sw.Options[0].(discord.ApplicationCommandOptionString)
	if !ok {
		t.Fatalf("switch option = %+v, want a string", sw.Options[0])
	}
	if choices := opt.Choices; len(choices) != 2 || choices[0].Value != bart || choices[1].Value != lisa {
		t.Errorf("switch choices = %+v", opt.Choices)
	}
}

func TestPreviewSounds(t *testing.T) {
	inOrder := func(n int) []int {
		p := make([]int, n)
		for i := range p {
			p[i] = i
		}
		return p
	}
	tests := []struct {
		name             string
		picked, playable []string
		want             []string
	}{
		{"picks first, topped up", []string{"c"}, []string{"a", "b", "c", "d"}, []string{"c", "a", "b"}},
		{"a pick that cannot play here is skipped", []string{"x", "b"}, []string{"a", "b"}, []string{"b", "a"}},
		{"never more than three", []string{"a", "b", "c"}, []string{"a", "b", "c", "d"}, []string{"a", "b", "c"}},
		{"no sound can play here", []string{"a"}, nil, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := previewSounds(tt.picked, tt.playable, inOrder); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSwitchCooldown(t *testing.T) {
	var g guildCooldown
	now := time.Now()
	if _, ok := g.take(1, now); !ok {
		t.Fatal("first switch refused")
	}
	if until, ok := g.take(1, now.Add(time.Minute)); ok || !until.Equal(now.Add(switchCooldown)) {
		t.Errorf("second switch: ok %v, until %v", ok, until)
	}
	if _, ok := g.take(2, now); !ok {
		t.Error("another guild was held by this one's switch")
	}
	if _, ok := g.take(1, now.Add(switchCooldown)); !ok {
		t.Error("still refused once the hour was up")
	}
}

// The link to a character's own bot only appears when it has one.
func TestOwnBot(t *testing.T) {
	if got := ownBot(&characters.Character{Profile: profile.Profile{ID: bart}}); got != "" {
		t.Errorf("no app: %q", got)
	}
	if got := ownBot(&characters.Character{Profile: profile.Profile{ID: bart, App: 42}}); got == "" {
		t.Error("with an app: no link")
	}
}

// One character's plays are not a breakdown; plays from before characters join the default's.
func TestByCharacter(t *testing.T) {
	c := &Commands{chars: charSet(t, bart, lisa)}
	if got := c.byCharacter([]store.Row{{Key: "", N: 3}, {Key: bart, N: 2}}); got != "" {
		t.Errorf("only the default played: %q, want nothing", got)
	}
	got := c.byCharacter([]store.Row{{Key: lisa, N: 4}, {Key: "", N: 3}, {Key: bart, N: 2}, {Key: "homer", N: 1}})
	if want := "bart · 5 plays\nlisa · 4 plays\nhomer · 1 play"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// A reply takes its server's character's color; where that has none, or there is no server, the
// bot's. Only info's color is swapped: a refusal stays red whoever the bot is.
func TestBranded(t *testing.T) {
	const own = 0x123456
	guild := snowflake.ID(1)
	for _, tt := range []struct {
		name  string
		color int
		guild *snowflake.ID
		want  int
	}{
		{"the character's", own, &guild, own},
		{"a character without one", 0, &guild, colBrand},
		{"no server", own, nil, colBrand},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s, err := characters.New([]profile.Profile{{ID: bart, Dir: t.TempDir(), Color: tt.color}}, bart)
			if err != nil {
				t.Fatal(err)
			}
			c := &Commands{chars: s, settings: settings.New(nil)}
			got := c.branded(tt.guild, []discord.Embed{info("", ""), bad("", "")})
			if got[0].Color != tt.want || got[1].Color != colBad {
				t.Errorf("colors = %#x, %#x; want %#x, %#x", got[0].Color, got[1].Color, tt.want, colBad)
			}
		})
	}
}
