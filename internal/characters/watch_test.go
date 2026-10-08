package characters

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A change is acted on once it has held still for watchQuiet, and only once.
func TestDebounce(t *testing.T) {
	now := time.Now()
	d := debounce{seen: "a"}
	steps := []struct {
		fp    string
		after time.Duration
		want  bool
	}{
		{"a", 0, false},
		{"b", 0, false},
		{"c", 5 * time.Second, false}, // still changing: the wait starts again
		{"c", watchQuiet - time.Second, false},
		{"c", watchQuiet + 5*time.Second, true},
		{"c", watchQuiet + 15*time.Second, false},
	}
	for i, s := range steps {
		if got := d.settled(s.fp, now.Add(s.after)); got != s.want {
			t.Errorf("step %d (%s at %s): settled = %v, want %v", i, s.fp, s.after, got, s.want)
		}
	}
}

// A profile.toml or an avatar changing is a change; a sound arriving is the registry's business.
func TestFingerprint(t *testing.T) {
	root := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("lisa/profile.toml", `id = "lisa"`)
	before := fingerprint(root)
	write("lisa/sounds/sax.ogg", "x")
	if fingerprint(root) != before {
		t.Error("a sound changed the fingerprint")
	}
	write("lisa/avatar.png", "x")
	if fingerprint(root) == before {
		t.Error("an avatar did not change the fingerprint")
	}
	before = fingerprint(root)
	write("lisa/profile.toml", `id = "lisa"`+"\n"+`nickname = "Lisa"`)
	if fingerprint(root) == before {
		t.Error("an edited profile did not change the fingerprint")
	}
}
