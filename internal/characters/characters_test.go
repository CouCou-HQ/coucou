package characters

import (
	"testing"

	"github.com/be-sandaa/coucou/internal/profile"
)

const bart, lisa, homer = "bart", "lisa", "homer"

func set(t *testing.T, def string, ids ...string) (*Set, error) {
	t.Helper()
	ps := make([]profile.Profile, len(ids))
	for i, id := range ids {
		ps[i] = profile.Profile{ID: id, Dir: t.TempDir()}
	}
	return New(ps, def)
}

func TestDefault(t *testing.T) {
	one, err := set(t, "", bart)
	if err != nil || one.Default().ID != bart {
		t.Fatalf("one character without a default: %v, %v", one, err)
	}
	two, err := set(t, lisa, bart, lisa)
	if err != nil || two.Default().ID != lisa {
		t.Fatalf("named default: %v, %v", two, err)
	}
	for _, tc := range []struct {
		name, def string
		ids       []string
	}{
		{"two without a default", "", []string{bart, lisa}},
		{"default not loaded", homer, []string{bart, lisa}},
		{"no characters", "", nil},
	} {
		if _, err := set(t, tc.def, tc.ids...); err == nil {
			t.Errorf("%s: expected an error", tc.name)
		}
	}
}

// A server that never picked, or picked a character that has since gone, gets the default.
func TestGet(t *testing.T) {
	s, err := set(t, lisa, bart, lisa)
	if err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]string{bart: bart, "": lisa, homer: lisa} {
		if got := s.Get(id).ID; got != want {
			t.Errorf("Get(%q) = %q, want %q", id, got, want)
		}
	}
}
