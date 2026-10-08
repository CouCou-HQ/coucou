package bot

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/omit"
	"github.com/disgoorg/snowflake/v2"

	"github.com/be-sandaa/coucou/internal/characters"
	"github.com/be-sandaa/coucou/internal/profile"
	"github.com/be-sandaa/coucou/internal/settings"
	"github.com/be-sandaa/coucou/internal/store"
)

const (
	personaGuild snowflake.ID = 7
	lisaNick                  = "Lisa"
	lisaHash                  = "1234abcd"
	bartNick                  = "Bart"
)

var errAvatarTooSoon = &rest.Error{
	Code:   rest.JSONErrorCodeInvalidFormBody,
	Errors: []byte(`{"avatar":{"_errors":[{"code":"AVATAR_RATE_LIMIT","message":"You are changing your avatar too fast. Try again later."}]}}`),
}

// persona is a Persona over bart and lisa, lisa the default with a nickname and an avatar, whose
// Discord calls are recorded and answered with err.
func persona(t *testing.T, pushed, nick string, err error) (*Persona, *[]discord.CurrentMemberUpdate, *time.Duration) {
	t.Helper()
	chars, cerr := characters.New([]profile.Profile{
		{ID: "bart", Nickname: bartNick, Dir: t.TempDir()},
		{ID: "lisa", Nickname: lisaNick, Dir: t.TempDir(), Avatar: &discord.Icon{Type: discord.IconTypePNG}, AvatarHash: lisaHash},
	}, "lisa")
	if cerr != nil {
		t.Fatal(cerr)
	}
	set := settings.New(&fakeStore{rows: []store.Settings{{Guild: personaGuild, PushedAvatar: pushed}}})
	if err := set.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	var calls []discord.CurrentMemberUpdate
	var retry time.Duration
	p := &Persona{
		chars: chars, set: set, pending: map[snowflake.ID]bool{},
		self: func(snowflake.ID) (string, bool) { return nick, true },
		update: func(_ snowflake.ID, u discord.CurrentMemberUpdate) error {
			calls = append(calls, u)
			if !u.Avatar.IsZero() {
				return err
			}
			return nil
		},
		after: func(d time.Duration, f func()) { retry = d; f() },
	}
	return p, &calls, &retry
}

func TestPersonaNickname(t *testing.T) {
	tests := []struct {
		name, nick string
		pushed     bool
	}{
		{"no nickname yet", "", true},
		{"another character's", bartNick, true},
		{"already right", lisaNick, false},
		{"chosen by the admins", "The Boss", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, calls, _ := persona(t, lisaHash, tt.nick, nil)
			p.push(t.Context(), personaGuild)
			if pushed := len(*calls) == 1 && (*calls)[0].Nick != nil && *(*calls)[0].Nick == lisaNick; pushed != tt.pushed || len(*calls) > 1 {
				t.Errorf("calls = %+v, want nickname pushed: %v", *calls, tt.pushed)
			}
		})
	}
}

func TestPersonaAvatar(t *testing.T) {
	t.Run("uploaded once and recorded", func(t *testing.T) {
		p, calls, _ := persona(t, "", lisaNick, nil)
		p.push(t.Context(), personaGuild)
		if len(*calls) != 1 || (*calls)[0].Avatar.IsZero() {
			t.Fatalf("calls = %+v, want one avatar upload", *calls)
		}
		if got := p.set.Get(personaGuild).PushedAvatar; got != lisaHash {
			t.Errorf("PushedAvatar = %q, want %q", got, lisaHash)
		}
		p.push(t.Context(), personaGuild)
		if len(*calls) != 1 {
			t.Errorf("pushed again with nothing changed: %+v", *calls)
		}
	})
	t.Run("too soon is retried, not recorded", func(t *testing.T) {
		p, _, retry := persona(t, "", lisaNick, errAvatarTooSoon)
		p.push(t.Context(), personaGuild)
		if *retry != avatarRetryAfter {
			t.Errorf("retry after %s, want %s", *retry, avatarRetryAfter)
		}
		if g, ok := p.next(); !ok || g != personaGuild {
			t.Error("the retry did not queue the guild again")
		}
		if got := p.set.Get(personaGuild).PushedAvatar; got != "" {
			t.Errorf("PushedAvatar = %q after a refusal, want it unrecorded", got)
		}
	})
	t.Run("any other failure is not retried", func(t *testing.T) {
		p, _, retry := persona(t, "", lisaNick, errors.New("boom"))
		p.push(t.Context(), personaGuild)
		if *retry != 0 {
			t.Errorf("retried after %s", *retry)
		}
	})
}

// Without an avatar the upload is a null, which clears the guild avatar; an omitted field would
// leave whatever the last character put there.
func TestNoAvatarIsSentAsNull(t *testing.T) {
	b, err := json.Marshal(discord.CurrentMemberUpdate{Avatar: omit.New[*discord.Icon](nil)})
	if err != nil || string(b) != `{"avatar":null}` {
		t.Errorf("got %s, %v; want {\"avatar\":null}", b, err)
	}
}

func TestPersonaQueueOncePerGuild(t *testing.T) {
	p, _, _ := persona(t, "", "", nil)
	p.Push(personaGuild, personaGuild, personaGuild+1)
	var got []snowflake.ID
	for g, ok := p.next(); ok; g, ok = p.next() {
		got = append(got, g)
	}
	if len(got) != 2 {
		t.Errorf("queue = %v, want each guild once", got)
	}
}

func TestAvatarLimited(t *testing.T) {
	other := &rest.Error{Code: rest.JSONErrorCodeInvalidFormBody, Errors: []byte(`{"nick":{"_errors":[{"code":"BASE_TYPE_MAX_LENGTH"}]}}`)}
	for err, want := range map[error]bool{errAvatarTooSoon: true, other: false, errors.New("x"): false} {
		if got := avatarLimited(err); got != want {
			t.Errorf("avatarLimited(%v) = %v, want %v", err, got, want)
		}
	}
	if avatarLimited(nil) {
		t.Error("avatarLimited(nil) = true")
	}
}
