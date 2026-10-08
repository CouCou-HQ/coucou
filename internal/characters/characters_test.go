package characters

import (
	"testing"

	"github.com/be-sandaa/coucou/internal/profile"
)

const bart, lisa, homer, lisaNick = "bart", "lisa", "homer", "Lisa"

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

// A reload keeps the registry of a character whose folder is the same, adds a new one and drops
// a removed one; a reload that cannot stand changes nothing.
func TestReload(t *testing.T) {
	bartDir, lisaDir := t.TempDir(), t.TempDir()
	s, err := New([]profile.Profile{{ID: bart, Dir: bartDir}, {ID: lisa, Dir: lisaDir}}, lisa)
	if err != nil {
		t.Fatal(err)
	}
	kept := s.Get(lisa).Sounds
	if err := s.Reload([]profile.Profile{{ID: lisa, Dir: lisaDir, Nickname: lisaNick}, {ID: homer, Dir: t.TempDir()}}); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if got := s.Get(lisa); got.Sounds != kept || got.Nickname != lisaNick {
		t.Errorf("lisa = %+v, want the same registry and the new nickname", got)
	}
	if s.Get(homer).ID != homer || s.Get(bart).ID != lisa {
		t.Errorf("homer added and bart gone: got %q and %q", s.Get(homer).ID, s.Get(bart).ID)
	}
	if err := s.Reload([]profile.Profile{{ID: bart, Dir: bartDir}}); err == nil {
		t.Error("reload without the default character: expected an error")
	}
	if len(s.All()) != 2 || s.Default().ID != lisa {
		t.Errorf("a refused reload changed the set: %+v", s.All())
	}
}

// Readers never see half a reload.
func TestReloadWhileReading(t *testing.T) {
	dir := t.TempDir()
	s, err := New([]profile.Profile{{ID: bart, Dir: dir}}, "")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 200 {
			_ = s.Get(bart).Sounds.Len()
			_ = s.Label("x")
		}
	}()
	for i := range 50 {
		if err := s.Reload([]profile.Profile{{ID: bart, Dir: dir, Nickname: string(rune('a' + i%26))}}); err != nil {
			t.Fatal(err)
		}
	}
	<-done
}
