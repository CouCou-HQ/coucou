package sounds

import (
	"context"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// A minimal first page that satisfies the sniff: "OggS" capture pattern with "OpusHead" inside the
// first 64 bytes, padded past the 36-byte floor the sniff requires.
func oggOpusBytes() []byte {
	b := make([]byte, 0, 64)
	b = append(b, "OggS"...)
	b = append(b, make([]byte, 24)...) // rest of the page header
	b = append(b, "OpusHead"...)
	b = append(b, make([]byte, 24)...)
	return b
}

const (
	// Short enough that the suite does not sleep through the real windows per file, long enough
	// that the two os.Stat calls either side of the settle still straddle a write in progress.
	testSettle   = 20 * time.Millisecond
	testDebounce = 20 * time.Millisecond
	// quiet is how long to wait before concluding nothing is going to be registered: one debounce
	// plus one settle, with slack for a loaded machine.
	quiet = testDebounce + testSettle + 500*time.Millisecond
)

// hurry turns both timing windows down. Call it before Start, which waits one of each itself.
func hurry(r *Registry) *Registry {
	r.settle, r.debounce = testSettle, testDebounce
	return r
}

// waitFor polls until cond holds or the deadline passes. Registration is inherently timed — a
// debounce then a settle window — so everything here is asserted with a deadline, never a sleep.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	const d = 5 * time.Second
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("timed out after %s waiting for %s", d, what)
}

const (
	soundName = "boom"
	unknown   = "nothing"
	fooName   = "foo"
	fooV2     = "foo.v2"
	rareZ     = "z.rare"
	fooTypo   = "foo.nswf"
)

// startRegistry brings a registry up on a fresh temp dir and returns it with the dir.
func startRegistry(t *testing.T) (*Registry, string) {
	t.Helper()
	dir := t.TempDir()
	r := hurry(New(dir))
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := r.Start(ctx, 0); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if r.Len() != 0 {
		t.Fatalf("fresh dir should be empty, got %d", r.Len())
	}
	return r, dir
}

func TestHalfCopiedFileIsNotRegistered(t *testing.T) {
	r, dir := startRegistry(t)
	full := oggOpusBytes()

	// Too short for the sniff and with no OpusHead yet: this is a copy in progress.
	if err := os.WriteFile(filepath.Join(dir, soundName+".ogg"), full[:20], 0o600); err != nil {
		t.Fatal(err)
	}
	time.Sleep(quiet)
	if _, ok := r.Path(soundName); ok {
		t.Fatal("a half-copied file must not be registered")
	}
}

func TestFinishedFileIsRegistered(t *testing.T) {
	r, dir := startRegistry(t)
	if err := os.WriteFile(filepath.Join(dir, soundName+".ogg"), oggOpusBytes(), 0o600); err != nil {
		t.Fatal(err)
	}

	waitFor(t, soundName+" to be registered", func() bool {
		_, ok := r.Path(soundName)
		return ok
	})
	if got := r.Names(false); len(got) != 1 || got[0] != soundName {
		t.Fatalf("Names() = %v, want [%s]", got, soundName)
	}
	if name, ok := r.Pick(false); !ok || name != soundName {
		t.Fatalf("Pick() = %q, %v; want %s, true", name, ok, soundName)
	}
}

func TestDeletedFileIsRemoved(t *testing.T) {
	r, dir := startRegistry(t)
	path := filepath.Join(dir, soundName+".ogg")
	if err := os.WriteFile(path, oggOpusBytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	waitFor(t, soundName+" to be registered", func() bool {
		_, ok := r.Path(soundName)
		return ok
	})

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	waitFor(t, soundName+" to be removed", func() bool {
		_, ok := r.Path(soundName)
		return !ok
	})
	if r.Len() != 0 {
		t.Fatalf("registry should be empty after delete, got %d", r.Len())
	}
}

func TestGarbageOggNeverRegisters(t *testing.T) {
	dir := t.TempDir()

	// A Vorbis stream: right container, wrong codec. And a file that is not Ogg at all.
	vorbis := append([]byte("OggS"), make([]byte, 24)...)
	vorbis = append(vorbis, 0x01, 'v', 'o', 'r', 'b', 'i', 's')
	vorbis = append(vorbis, make([]byte, 32)...)
	if err := os.WriteFile(filepath.Join(dir, "vorbis.ogg"), vorbis, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "junk.ogg"), []byte("this is not a container at all, not even close"), 0o600); err != nil {
		t.Fatal(err)
	}

	r := hurry(New(dir))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := r.Start(ctx, 0); err != nil {
		t.Fatalf("Start: %v", err)
	}
	time.Sleep(quiet)

	if n := r.Len(); n != 0 {
		t.Fatalf("registered %d non-Opus files: %v", n, r.Names(false))
	}
	if _, ok := r.Pick(false); ok {
		t.Error("Pick() should report no sounds")
	}
}

// newFixed builds a registry over a known file set without starting the watcher: the two tests
// below are about lookup and ordering rather than registration, and going through Start would cost
// a settle window each.
// Each argument is a file name without ".ogg", so "zap.rare" is the rare "zap".
func newFixed(t *testing.T, files ...string) *Registry {
	t.Helper()
	r := New(t.TempDir())
	for _, f := range files {
		name, tags, ok := parse(f + ext)
		if !ok {
			t.Fatalf("parse(%q) failed", f+ext)
		}
		r.files[name] = entry{path: filepath.Join(r.dir, f+ext), tags: tags}
	}
	return r
}

func TestNamesSortedAndStable(t *testing.T) {
	r := newFixed(t, "zap", "Boom", "arc")

	want := []string{"arc", "Boom", "zap"}
	got := r.Names(false)
	if !slices.Equal(got, want) {
		t.Errorf("Names() = %v, want %v", got, want)
	}
	if again := r.Names(false); !slices.Equal(again, got) {
		t.Errorf("Names() gave %v then %v; the list must not move between calls", got, again)
	}
}

func TestPathMatchesRegardlessOfCase(t *testing.T) {
	const file = "Fart-Loud"
	r := newFixed(t, file)

	for _, q := range []string{file, "fart-loud", "FART-LOUD", "Fart-loud"} {
		if _, ok := r.Path(q); !ok {
			t.Errorf("Path(%q) missed, but %s.ogg is registered", q, file)
		}
	}
	if _, ok := r.Path(unknown); ok {
		t.Error(`Path(unknown) hit; no such sound is registered`)
	}
}

func TestPickOther(t *testing.T) {
	tests := []struct {
		name   string
		sounds []string
		want   string
		ok     bool
	}{
		{"the only sound has no other", []string{"a"}, "", false},
		{"nothing registered", nil, "", false},
		{"never the one it was given", []string{"a", "b"}, "b", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newFixed(t, tt.sounds...)
			for range 50 {
				if got, ok := r.PickOther("a", false); got != tt.want || ok != tt.ok {
					t.Fatalf("PickOther(a) = %q, %v; want %q, %v", got, ok, tt.want, tt.ok)
				}
			}
		})
	}
}

// OnChange runs outside the registry's lock, so a callback that reads the registry back — the
// natural thing for one to do — cannot deadlock it.
func TestOnChangeCanReadTheRegistry(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, soundName+".ogg")
	if err := os.WriteFile(path, oggOpusBytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	r := hurry(New(dir))
	removed := make(chan int, 1)
	r.OnChange = func(_ string, added bool) {
		if !added {
			removed <- r.Len()
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := r.Start(ctx, 0); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitFor(t, soundName+" to be registered", func() bool {
		_, ok := r.Path(soundName)
		return ok
	})

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	select {
	case n := <-removed:
		if n != 0 {
			t.Fatalf("Len() inside OnChange = %d, want 0", n)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("OnChange never returned: it is being called under the registry's lock")
	}
}

func TestParse(t *testing.T) {
	tests := []struct {
		file string
		name string
		tags tags
		ok   bool
	}{
		{"foo.ogg", fooName, 0, true},
		{"foo.rare.ogg", fooName, tagRare, true},
		{"foo.nsfw.ogg", fooName, tagNSFW, true},
		{"foo.rare.nsfw.ogg", fooName, tagRare | tagNSFW, true},
		{"foo.nsfw.rare.ogg", fooName, tagRare | tagNSFW, true},
		{"foo.v2.ogg", fooV2, 0, true},
		{"foo.v2.rare.ogg", fooV2, tagRare, true},
		{"foo.rare.v2.ogg", "foo.rare.v2", 0, true},
		{fooTypo + ext, fooTypo, 0, true},
		{nameRare + ext, nameRare, 0, true},
		{".rare.ogg", ".rare", 0, true},
		{"nsfw.rare.ogg", nameNSFW, tagRare, true},
		{"foo.rare.rare.ogg", fooName, tagRare, true},
		{".ogg", "", 0, false},
		{"foo.mp3", "", 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			name, tags, ok := parse(tt.file)
			if name != tt.name || tags != tt.tags || ok != tt.ok {
				t.Errorf("parse(%q) = %q, %v, %v; want %q, %v, %v", tt.file, name, tags, ok, tt.name, tt.tags, tt.ok)
			}
		})
	}
}

func TestTypo(t *testing.T) {
	tests := map[string]string{
		fooTypo:     nameNSFW,
		"foo.rate":  nameRare,
		"foo.raer":  nameRare,
		"foo.nsfx":  nameNSFW,
		fooV2:       "",
		"foo.rares": "",
		"foo":       "",
		".rate":     "",
		"foo.nsfw":  "",
	}
	for name, want := range tests {
		t.Run(name, func(t *testing.T) {
			if got := typo(name); got != want {
				t.Errorf("typo(%q) = %q, want %q", name, got, want)
			}
		})
	}
}

// nsfw sounds exist only where they are allowed; rare-and-nsfw follows both rules.
func TestNSFWOnlyWhereAllowed(t *testing.T) {
	r := newFixed(t, "a", "x.nsfw", "y.rare.nsfw")
	tests := []struct {
		nsfw     bool
		names    []string
		playable map[string]bool
	}{
		{false, []string{"a"}, map[string]bool{"a": true, "x": false, "y": false}},
		{true, []string{"a", "x"}, map[string]bool{"a": true, "x": true, "y": false}},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("nsfw=%v", tt.nsfw), func(t *testing.T) {
			if got := r.Names(tt.nsfw); !slices.Equal(got, tt.names) {
				t.Errorf("Names(%v) = %v, want %v", tt.nsfw, got, tt.names)
			}
			for n, want := range tt.playable {
				if got := r.Playable(n, tt.nsfw); got != want {
					t.Errorf("Playable(%q, %v) = %v, want %v", n, tt.nsfw, got, want)
				}
			}
			seen := map[string]bool{}
			for range 2000 {
				n, _ := r.draw(tt.nsfw)
				seen[n] = true
			}
			if seen["x"] != tt.nsfw || seen["y"] != tt.nsfw {
				t.Errorf("draw(%v) reached %v", tt.nsfw, seen)
			}
		})
	}
}

func TestRaresCannotBeAskedForByName(t *testing.T) {
	r := newFixed(t, soundName, rareZ)

	if got := r.Names(false); !slices.Equal(got, []string{soundName}) {
		t.Errorf("Names() = %v, want [%s]", got, soundName)
	}
	tests := []struct {
		name     string
		playable bool
	}{
		{soundName, true},
		{"z", false},
		{"Z", false},
		{unknown, false},
	}
	for _, tt := range tests {
		if got := r.Playable(tt.name, false); got != tt.playable {
			t.Errorf("Playable(%q) = %v, want %v", tt.name, got, tt.playable)
		}
	}
	if _, ok := r.Path("z"); !ok {
		t.Error(`Path("z") missed: a rolled rare still has to resolve to its file`)
	}
}

func seeded(r *Registry) *Registry {
	r.intN = rand.New(rand.NewPCG(1, 2)).IntN
	return r
}

func TestDrawWeightsRaresAtATenth(t *testing.T) {
	r := seeded(newFixed(t, "a", "b", rareZ))
	const draws = 100_000
	counts := map[string]int{}
	for range draws {
		n, _ := r.draw(false)
		counts[n]++
	}
	// Expected share of z is weightRare/(2*weightNormal+weightRare) = 1/21 ≈ 4.76%.
	want := float64(draws) * weightRare / (2*weightNormal + weightRare)
	if got := float64(counts["z"]); got < want*0.9 || got > want*1.1 {
		t.Errorf("rare drawn %v times in %d, want about %.0f (counts %v)", got, draws, want, counts)
	}
}

// scripted hands out xs in order, one per intN call.
func scripted(t *testing.T, xs ...int) func(int) int {
	t.Helper()
	return func(int) int {
		if len(xs) == 0 {
			t.Fatal("intN called more often than scripted")
		}
		x := xs[0]
		xs = xs[1:]
		return x
	}
}

// With "a" and "b" at weight 10 each, 0-9 draws a and 10-19 draws b.
func TestPickRedrawsARepeatOnce(t *testing.T) {
	tests := []struct {
		name   string
		script []int
		want   []string
	}{
		{"no repeat, one draw each", []int{0, 10}, []string{"a", "b"}},
		{"repeat re-drawn to the other", []int{0, 0, 10}, []string{"a", "b"}},
		{"second draw repeats and is kept", []int{0, 0, 0}, []string{"a", "a"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newFixed(t, "a", "b")
			r.intN = scripted(t, tt.script...)
			for i, want := range tt.want {
				if n, ok := r.Pick(false); !ok || n != want {
					t.Fatalf("Pick() #%d = %q, %v; want %s, true", i+1, n, ok, want)
				}
			}
		})
	}
}

func TestPickWithOneSoundRepeatsIt(t *testing.T) {
	r := newFixed(t, "a")
	for range 3 {
		if n, ok := r.Pick(false); !ok || n != "a" {
			t.Fatalf("Pick() = %q, %v; want a, true", n, ok)
		}
	}
}

func TestCollection(t *testing.T) {
	r := newFixed(t, "a", "b", "c", "y.rare", rareZ, "n.nsfw", "m.rare.nsfw")
	tests := []struct {
		name  string
		heard []string
		nsfw  bool
		want  Collection
	}{
		{"none heard", nil, false, Collection{0, 3, 0, 2}},
		{"some of each", []string{"a", "c", "z"}, false, Collection{2, 3, 1, 2}},
		{"deleted files count for nothing", []string{"a", "gone", "old"}, false, Collection{1, 3, 0, 2}},
		{"everything", []string{"a", "b", "c", "y", "z"}, false, Collection{3, 3, 2, 2}},
		{"nsfw heard elsewhere counts for nothing", []string{"a", "n", "m"}, false, Collection{1, 3, 0, 2}},
		{"nsfw counts where allowed", []string{"a", "n", "m"}, true, Collection{2, 4, 1, 3}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := r.Collection(tt.heard, tt.nsfw); got != tt.want {
				t.Errorf("Collection(%v) = %+v, want %+v", tt.heard, got, tt.want)
			}
		})
	}
}

// Both the inotify and the polling path: a rare and a normal file for one name register the rare,
// and deleting the rare falls back to the normal one.
func TestDuplicateNameRareWinsAndFallsBack(t *testing.T) {
	tests := []struct {
		name string
		poll time.Duration
	}{
		{"inotify", 0},
		{"poll", 50 * time.Millisecond},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			normal := filepath.Join(dir, soundName+ext)
			rare := filepath.Join(dir, soundName+".rare"+ext)
			if err := os.WriteFile(normal, oggOpusBytes(), 0o600); err != nil {
				t.Fatal(err)
			}
			r := hurry(New(dir))
			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			if err := r.Start(ctx, tt.poll); err != nil {
				t.Fatalf("Start: %v", err)
			}
			waitFor(t, soundName+" as a normal sound", func() bool { return r.Playable(soundName, false) })

			if err := os.WriteFile(rare, oggOpusBytes(), 0o600); err != nil {
				t.Fatal(err)
			}
			waitFor(t, "the rare file to win", func() bool {
				p, _ := r.Path(soundName)
				return p == rare
			})
			if r.Playable(soundName, false) || r.Len() != 1 {
				t.Fatalf("Playable = %v, Len = %d; want the one rare entry", r.Playable(soundName, false), r.Len())
			}

			if err := os.Remove(rare); err != nil {
				t.Fatal(err)
			}
			waitFor(t, soundName+" to fall back to "+filepath.Base(normal), func() bool {
				p, _ := r.Path(soundName)
				return p == normal && r.Playable(soundName, false)
			})
		})
	}
}

// The most-tagged file wins a clash, whatever order its tags are written in, and each deletion
// falls back one step.
func TestClashMostTaggedWins(t *testing.T) {
	dir := t.TempDir()
	r := hurry(New(dir))
	files := []string{"boom.nsfw.rare", "boom.nsfw", "boom.rare", "boom"}
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(dir, f+ext), oggOpusBytes(), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range files {
		t.Run(f, func(t *testing.T) {
			r.consider(soundName)
			if p, _ := r.Path(soundName); p != filepath.Join(dir, f+ext) {
				t.Fatalf("Path(%s) = %s, want %s", soundName, filepath.Base(p), f+ext)
			}
			if err := os.Remove(filepath.Join(dir, f+ext)); err != nil {
				t.Fatal(err)
			}
		})
	}
	r.consider(soundName)
	if r.Len() != 0 {
		t.Fatalf("Len() = %d after every file went, want 0", r.Len())
	}
}

func TestDisplay(t *testing.T) {
	tests := map[string]string{
		"big_burp":      "Big Burp",
		"wet_fart_2":    "Wet Fart 2",
		"ufufu":         "Ufufu",
		"double__under": "Double Under",
		"foo.v2":        "Foo.v2",
		"élan_vital":    "Élan Vital",
		"":              "",
	}
	for in, want := range tests {
		if got := Display(in); got != want {
			t.Errorf("Display(%q) = %q, want %q", in, got, want)
		}
	}
}

// Markers come from the loaded file's tags, rare before nsfw; a name no longer loaded has no file
// to read them from, so it shows bare.
func TestLabel(t *testing.T) {
	r := newFixed(t, "nice_dog", "perfect_fart.rare", "hush.nsfw", "both.nsfw.rare")
	tests := map[string]string{
		"nice_dog":     "Nice Dog",
		"perfect_fart": "Perfect Fart " + MarkRare,
		"hush":         "Hush " + MarkNSFW,
		"both":         "Both " + MarkRare + " " + MarkNSFW,
		"deleted_one":  "Deleted One",
	}
	for in, want := range tests {
		if got := r.Label(in); got != want {
			t.Errorf("Label(%q) = %q, want %q", in, got, want)
		}
	}
}

// /play also takes a typed name, and people type what the list showed them.
func TestLookupTakesTheRenderedName(t *testing.T) {
	r := New(t.TempDir())
	r.files["wet_fart_2"] = entry{path: "wet_fart_2.ogg"}
	if _, ok := r.lookup("Wet Fart 2"); !ok {
		t.Error(`lookup("Wet Fart 2") found nothing, want wet_fart_2`)
	}
}
