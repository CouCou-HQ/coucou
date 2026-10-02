package commands

import (
	"strings"
	"testing"

	"github.com/disgoorg/disgo/discord"
)

// embedDescriptionLimit is Discord's cap on an embed description. Past it the whole message is
// rejected rather than clipped, so /help growing too big would break it outright.
const embedDescriptionLimit = 4096

func helpBody() string { return "**Commands**\n" + commandList() + "\n" + helpLimits }

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
	body := helpBody()
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

// One embed, so it has to fit in one embed.
func TestHelpFitsOneEmbed(t *testing.T) {
	if n := len([]rune(helpBody())); n > embedDescriptionLimit {
		t.Errorf("help body is %d characters, over Discord's %d", n, embedDescriptionLimit)
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
