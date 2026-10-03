package profile

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/disgoorg/disgo/discord"

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
		Status:   Status{Text: "🖤 lurking", Activity: discord.ActivityTypeListening, Online: discord.OnlineStatusDND},
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
		{"unknown activity", "id = \"x\"\n[status]\nactivity = \"streaming\""},
		{"unknown online state", "id = \"x\"\n[status]\nonline = \"invisible\""},
		{"status text over 128 characters", "id = \"x\"\n[status]\ntext = \"" + strings.Repeat("🖤", MaxStatus+1) + "\""},
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
