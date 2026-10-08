package commands

import (
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"

	"github.com/be-sandaa/coucou/internal/voice"
)

func inviteQuery(t *testing.T) url.Values {
	t.Helper()
	u, err := url.Parse(inviteURL(snowflake.ID(1234)))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := u.Scheme + "://" + u.Host + u.Path; got != "https://discord.com/oauth2/authorize" {
		t.Fatalf("authorize endpoint is %q", got)
	}
	return u.Query()
}

// Permissions has a String method, so the constant rendered through fmt.Sprint asks Discord to
// authorize "View Channel, Connect, Speak, ..." and is rejected. This is the test for that one mistake.
func TestInviteAsksForTheBitsNotTheNames(t *testing.T) {
	got := inviteQuery(t).Get("permissions")
	if _, err := strconv.ParseInt(got, 10, 64); err != nil {
		t.Fatalf("permissions=%q is not an integer", got)
	}
	if want := strconv.FormatInt(int64(invitePermissions), 10); got != want {
		t.Errorf("permissions=%s, want %s — the link must ask for exactly what playing and the welcome need", got, want)
	}
}

// The invite, pinned like voice.Needed below: the docs and every invite link in them hard-code 3165184.
func TestInviteIsVoiceAndTheWelcome(t *testing.T) {
	want := voice.Needed | discord.PermissionSendMessages | discord.PermissionEmbedLinks
	if invitePermissions != want || int64(want) != 3165184 {
		t.Errorf("invitePermissions = %d (%s), want %d", invitePermissions, invitePermissions, int64(want))
	}
}

// Added without applications.commands the bot joins and answers nothing, which looks like a broken
// bot rather than a missing scope.
func TestInviteAsksForBothScopes(t *testing.T) {
	scope := inviteQuery(t).Get("scope")
	for _, want := range []string{"bot", "applications.commands"} {
		if !slices.Contains(strings.Fields(scope), want) {
			t.Errorf("scope=%q is missing %q", scope, want)
		}
	}
}

func TestInviteCarriesTheApplicationID(t *testing.T) {
	if got := inviteQuery(t).Get("client_id"); got != "1234" {
		t.Errorf("client_id=%q, want 1234", got)
	}
}

// The set itself, pinned: widening it is a decision about what the bot may do in someone's server,
// not a refactor, and it should fail here first.
func TestNeededIsTheThreeVoicePermissions(t *testing.T) {
	want := discord.PermissionViewChannel | discord.PermissionConnect | discord.PermissionSpeak
	if voice.Needed != want {
		t.Errorf("voice.Needed = %s, want %s", voice.Needed, want)
	}
}
