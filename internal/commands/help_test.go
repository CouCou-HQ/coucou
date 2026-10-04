package commands

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"

	"github.com/be-sandaa/coucou/internal/profile"
	"github.com/be-sandaa/coucou/internal/settings"
)

// embedDescriptionLimit is Discord's cap on an embed description. Past it the whole message is
// rejected rather than clipped, so /help growing too big would break it outright.
const embedDescriptionLimit = 4096

func helpEmbeds(friends []discord.Embed) []discord.Embed {
	return fitHelp([]discord.Embed{
		info("The Bot", commandList()),
		info("Keeping the bot out", helpLimits),
		info("Sounds", helpSounds+"\n\n"+adultHelp(settings.NSFWRestricted, false)+"\n"+helpNSFW),
	}, friends)
}

// The point of generating the list is that a command added to definitions shows up in /help
// without anyone remembering to add it. This is the test that keeps that true.
func TestCommandListCoversEveryDefinition(t *testing.T) {
	list := commandList()
	for _, d := range definitions {
		c, ok := d.(discord.SlashCommandCreate)
		if !ok {
			continue
		}
		if !strings.Contains(list, "`/"+c.Name+"`") {
			t.Errorf("/%s is registered but missing from /help", c.Name)
		}
		if !strings.Contains(list, c.Description) {
			t.Errorf("/%s is listed without its description %q", c.Name, c.Description)
		}
	}
}

// The permission tag is read off the definition rather than written out, so it cannot drift from
// what Discord actually enforces.
func TestCommandListTagsTheGatedCommands(t *testing.T) {
	for _, line := range strings.Split(strings.TrimSpace(commandList()), "\n") {
		name, _, _ := strings.Cut(strings.TrimPrefix(line, "`/"), "`")

		var gated bool
		for _, d := range definitions {
			if c, ok := d.(discord.SlashCommandCreate); ok && c.Name == name {
				gated = !c.DefaultMemberPermissions.IsZero() && c.DefaultMemberPermissions.Value != nil
			}
		}

		if tagged := strings.Contains(line, "*Manage Server*"); tagged != gated {
			t.Errorf("/%s is tagged %v but gated %v: %q", name, tagged, gated, line)
		}
	}
}

// The three levers are the reason /help exists — denying the wrong one is how an admin concludes
// the bot is broken — so each has to actually be named.
func TestHelpExplainsAllThreeLevers(t *testing.T) {
	body := helpLimits
	for _, want := range []string{"Connect", "Integrations", "/optout on"} {
		if !strings.Contains(body, want) {
			t.Errorf("/help does not mention %q", want)
		}
	}
	// Naming them is not enough: the two that get confused have to say which one they are not.
	if !strings.Contains(body, "does not stop drop-ins") {
		t.Error("/help does not say that Integrations leaves drop-ins alone")
	}
}

// Every embed fits its own description cap, and with as many friends as fit, the reply stays inside
// Discord's ten embeds and its 6000 characters across them.
func TestHelpFits(t *testing.T) {
	siblings := make([]Sibling, 0, maxFriends)
	for i := range maxFriends {
		siblings = append(siblings, Sibling{Name: strings.Repeat("w", 80), App: snowflake.ID(100 + i)})
	}
	got := helpEmbeds(friendsGrid(siblings, 1, strings.Repeat("n", 32)))
	if len(got) != 4 || len(got[3].Fields) == 0 || len(got[3].Fields) == maxFriends {
		t.Errorf("/help kept %d embeds, want the grid trimmed rather than whole or gone", len(got))
	}
	if len(got) > 10 {
		t.Errorf("/help is %d embeds, over Discord's 10", len(got))
	}
	if n := messageSize(got); n > messageLimit {
		t.Errorf("/help is %d characters, over Discord's %d", n, messageLimit)
	}
	for _, e := range got {
		if n := utf8.RuneCountInString(e.Description); n > embedDescriptionLimit {
			t.Errorf("%q is %d characters, over Discord's %d", e.Title, n, embedDescriptionLimit)
		}
	}
}

// The fixed embeds always go out, however much of the friends grid has to give way.
func TestFitHelpDropsFriendsFirst(t *testing.T) {
	huge := info("", strings.Repeat("x", messageLimit))
	if got := helpEmbeds([]discord.Embed{huge, huge}); len(got) != 3 {
		t.Errorf("fitHelp kept %d embeds, want the three fixed ones", len(got))
	}
}

// /help advertises everything in definitions, so a command listed there with no handler is /help
// promising something that silently does nothing.
func TestEveryDefinitionHasAHandler(t *testing.T) {
	h := (&Commands{}).handlers()
	for _, d := range definitions {
		c, ok := d.(discord.SlashCommandCreate)
		if !ok {
			continue
		}
		if _, found := h[c.Name]; !found {
			t.Errorf("/%s is defined and listed in /help but has no handler", c.Name)
		}
	}
}

// /about is the character sheet: each part shows only when the profile has it, and a profile with
// none of them still answers rather than sending an empty embed Discord would reject.
func TestAboutBody(t *testing.T) {
	full := aboutBody(profile.Profile{Tagline: "Mean, nicely.", Lore: "Came anyway.", Traits: []string{"sits in silence", "leaves"}})
	if want := "*Mean, nicely.*\n\nCame anyway.\n\n**Traits**\n• sits in silence\n• leaves"; full != want {
		t.Errorf("aboutBody = %q, want %q", full, want)
	}
	const onlyLore = "Only lore."
	if got := aboutBody(profile.Profile{Lore: onlyLore}); got != onlyLore {
		t.Errorf("aboutBody(lore only) = %q", got)
	}
	if got := aboutBody(profile.Profile{}); got == "" {
		t.Error("aboutBody(empty) is empty, which Discord rejects")
	}
	if got := aboutBody(profile.Profile{Lore: strings.Repeat("x", 5000)}); utf8.RuneCountInString(got) > embedDescriptionLimit {
		t.Errorf("aboutBody of long lore is %d runes, over Discord's 4096", utf8.RuneCountInString(got))
	}
}
