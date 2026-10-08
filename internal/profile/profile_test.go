package profile

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/disgoorg/disgo/discord"

	"github.com/be-sandaa/coucou/internal/sounds"
	"github.com/be-sandaa/coucou/internal/store"
)

// write puts body in a temp dir as profile.toml and returns the dir.
func write(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "profile.toml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return filepath.Dir(path)
}

// Only id is required. Everything else falls back to what a bot without a character did before
// profiles existed: the brand hue, a 5% chance, and the other three settings off.
func TestMinimalProfile(t *testing.T) {
	dir := write(t, `id = "gus"`)
	got, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := Profile{
		Dir: dir, ID: "gus", Color: DefaultColor, Defaults: store.Defaults{Chance: defaultChance},
		Status: Status{Activity: discord.ActivityTypeCustom, Online: discord.OnlineStatusOnline},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %+v\nwant %+v", got, want)
	}
	if got.SoundsDir() != filepath.Join(dir, "sounds") {
		t.Errorf("SoundsDir = %q, want sounds/ beside profile.toml", got.SoundsDir())
	}
}

func TestEveryKeyIsRead(t *testing.T) {
	dir := write(t, `
id       = "lenore"
nickname = " Lenore "
application_id = "912694340814516254"
emoji    = "🖤"
color    = "#4E5058"
tagline  = "Mean, nicely."
lore     = """
Came anyway.
"""
traits   = ["sits in silence", "leaves without a sound"]

[defaults]
chance   = 0
suspense = 20
fakeout  = 50
encore   = 50

[[chains]]
chance = 80
steps  = [{ sound = "knock" }, { sound = "who", after = 2 }, { sound = "rim" }]

[[chains]]
steps = [{ sound = "drum" }, { sound = "clap" }]

[[links]]
from = "snare"
to   = { boo = 70, sad-trombone = 30 }

[status]
text     = " 🖤 lurking "
activity = "listening"
online   = "dnd"
`)
	got, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := Profile{
		Dir: dir, ID: "lenore", Nickname: "Lenore", Emoji: "🖤", Color: 0x4E5058,
		Tagline: "Mean, nicely.", Lore: "Came anyway.", Traits: []string{"sits in silence", "leaves without a sound"},
		// An explicit 0 chance is opt-in, not "unset": it must not fall back to 5.
		Defaults: store.Defaults{Chance: 0, Suspense: MaxSuspense, FakeOut: MaxFakeOut, Encore: MaxEncore},
		Chains: []sounds.Chain{
			{Chance: 80, Steps: []sounds.Step{{Sound: "knock"}, {Sound: "who", After: 2 * time.Second}, {Sound: "rim"}}},
			// Left out, a chain's chance is 100, not 0: listing one means wanting it to play.
			{Chance: 100, Steps: []sounds.Step{{Sound: "drum"}, {Sound: "clap"}}},
		},
		Links:  sounds.Links{"snare": {"boo": 70, "sad-trombone": 30}},
		Status: Status{Text: "🖤 lurking", Activity: discord.ActivityTypeListening, Online: discord.OnlineStatusDND},
		App:    912694340814516254,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %+v\nwant %+v", got, want)
	}
}

// The example people copy has to load as it stands.
func TestExampleProfileLoads(t *testing.T) {
	if _, err := load("../../profile.example.toml", t.TempDir()); err != nil {
		t.Errorf("Load: %v", err)
	}
}

func TestInvalidProfiles(t *testing.T) {
	tests := []struct{ name, body string }{
		{"no id", `nickname = "x"`},
		{"id with capitals", `id = "Lenore"`},
		{"id with a space", `id = "le nore"`},
		{"unknown key", "id = \"x\"\nname = \"x\""},
		{"unknown default", "id = \"x\"\n[defaults]\nvolume = 3"},
		{"colour without #", "id = \"x\"\ncolor = \"4E5058\""},
		{"colour too short", "id = \"x\"\ncolor = \"#FFF\""},
		{"colour not hex", "id = \"x\"\ncolor = \"#GGGGGG\""},
		{"chance above 100", "id = \"x\"\n[defaults]\nchance = 101"},
		{"chance negative", "id = \"x\"\n[defaults]\nchance = -1"},
		{"suspense above 20", "id = \"x\"\n[defaults]\nsuspense = 21"},
		{"fakeout above 50", "id = \"x\"\n[defaults]\nfakeout = 51"},
		{"encore above 50", "id = \"x\"\n[defaults]\nencore = 51"},
		{"chain of one step", "id = \"x\"\n[[chains]]\nsteps = [{ sound = \"a\" }]"},
		{"chain chance above 100", "id = \"x\"\n[[chains]]\nchance = 101\nsteps = [{ sound = \"a\" }, { sound = \"b\" }]"},
		{"chain step without a sound", "id = \"x\"\n[[chains]]\nsteps = [{ sound = \"a\" }, { after = 1 }]"},
		{"chain gap above 20", "id = \"x\"\n[[chains]]\nsteps = [{ sound = \"a\" }, { sound = \"b\", after = 21 }]"},
		{"unknown chain step key", "id = \"x\"\n[[chains]]\nsteps = [{ sound = \"a\" }, { sound = \"b\", delay = 1 }]"},
		{"two chains, one opener", "id = \"x\"\n[[chains]]\nsteps = [{ sound = \"a\" }, { sound = \"b\" }]\n[[chains]]\nsteps = [{ sound = \"a\" }, { sound = \"c\" }]"},
		{"link without from", "id = \"x\"\n[[links]]\nto = { b = 1 }"},
		{"link to nothing", "id = \"x\"\n[[links]]\nfrom = \"a\""},
		{"link weight 0", "id = \"x\"\n[[links]]\nfrom = \"a\"\nto = { b = 0 }"},
		{"linked from twice", "id = \"x\"\n[[links]]\nfrom = \"a\"\nto = { b = 1 }\n[[links]]\nfrom = \"a\"\nto = { c = 1 }"},
		{"unknown activity", "id = \"x\"\n[status]\nactivity = \"streaming\""},
		{"unknown online state", "id = \"x\"\n[status]\nonline = \"invisible\""},
		{"status text over 128 characters", "id = \"x\"\n[status]\ntext = \"" + strings.Repeat("🖤", MaxStatus+1) + "\""},
		{"application id not a number", "id = \"x\"\napplication_id = \"lenore\""},
		{"not toml", "id ="},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Load(write(t, tc.body)); err == nil {
				t.Error("expected an error")
			}
		})
	}
}

func TestMissingProfileIsAnError(t *testing.T) {
	if _, err := Load(t.TempDir()); err == nil {
		t.Fatal("expected an error for a directory with no profile.toml")
	}
}

// character puts a profile.toml with body under root/sub.
func character(t *testing.T, root, sub, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, sub), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, sub, "profile.toml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// Every subdirectory with a profile.toml is a character, sorted by id rather than by folder; a
// folder without one is skipped, so a stray directory is not an error.
func TestLoadAll(t *testing.T) {
	root := t.TempDir()
	character(t, root, "a", `id = "zelda"`)
	character(t, root, "b", `id = "bart"`)
	if err := os.MkdirAll(filepath.Join(root, "empty"), 0o750); err != nil {
		t.Fatal(err)
	}
	got, err := LoadAll(root)
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if len(got) != 2 || got[0].ID != "bart" || got[1].ID != "zelda" {
		t.Fatalf("got %+v, want bart then zelda", got)
	}
	if got[0].Dir != filepath.Join(root, "b") {
		t.Errorf("Dir = %q, want its own folder", got[0].Dir)
	}
}

func TestLoadAllRefuses(t *testing.T) {
	t.Run("no characters", func(t *testing.T) {
		if _, err := LoadAll(t.TempDir()); err == nil {
			t.Error("expected an error")
		}
	})
	t.Run("one id twice", func(t *testing.T) {
		root := t.TempDir()
		character(t, root, "a", `id = "bart"`)
		character(t, root, "b", `id = "bart"`)
		if _, err := LoadAll(root); err == nil {
			t.Error("expected an error")
		}
	})
	t.Run("one broken character", func(t *testing.T) {
		root := t.TempDir()
		character(t, root, "a", `id = "bart"`)
		character(t, root, "b", `id = "Bad"`)
		if _, err := LoadAll(root); err == nil {
			t.Error("expected an error")
		}
	})
}
