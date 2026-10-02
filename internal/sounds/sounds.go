// Package sounds keeps a live registry of Ogg Opus files in a directory.
// Add a file → it's playable within ~1.5 s. Delete it → gone. No restarts.
package sounds

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/fsnotify/fsnotify"
)

const (
	defaultSettle   = time.Second            // size must be stable this long before a file is registered
	defaultDebounce = 250 * time.Millisecond // editors and scp fire several events per write
)

const (
	ext      = ".ogg"
	tierRare = "rare"

	weightNormal = 10
	weightRare   = 1
)

type entry struct {
	path string
	rare bool
}

type Registry struct {
	dir string
	// OnChange, if set, is called with (name, added) on every registry change. The bot uses it to publish.
	OnChange func(name string, added bool)
	// settle and debounce are the two windows a file waits out before it is registered. Fields
	// rather than constants so tests can turn them down: at their real values every test that
	// registers a sound sleeps through both.
	settle   time.Duration
	debounce time.Duration
	intN     func(n int) int
	mu       sync.RWMutex
	files    map[string]entry
	last     string   // the name Pick handed out last, so it is not handed out twice running
	pending  sync.Map // name → *time.Timer (debounce)
}

func New(dir string) *Registry {
	return &Registry{
		dir:      dir,
		files:    map[string]entry{},
		intN:     rand.IntN,
		settle:   defaultSettle,
		debounce: defaultDebounce,
	}
}

// parse splits a file name into the sound name and whether it is rare: "foo.rare.ogg" is the rare
// "foo", and a middle segment that is not a tier stays in the name.
func parse(file string) (name string, rare, ok bool) {
	base, ok := strings.CutSuffix(file, ext)
	if !ok || base == "" {
		return "", false, false
	}
	if i := strings.LastIndex(base, "."); i > 0 && base[i+1:] == tierRare {
		return base[:i], true, true
	}
	return base, false, true
}

// Display renders a snake_case sound name for people: "marta_moan_2" is "Marta Moan 2".
func Display(name string) string {
	words := strings.FieldsFunc(name, func(r rune) bool { return r == '_' })
	for i, w := range words {
		r, size := utf8.DecodeRuneInString(w)
		words[i] = string(unicode.ToUpper(r)) + w[size:]
	}
	return strings.Join(words, " ")
}

// Names lists what can be asked for by name: rares are left out, so they only ever arrive by roll.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.files))
	for n, e := range r.files {
		if !e.rare {
			out = append(out, n)
		}
	}
	// Map order would reshuffle the autocomplete list under the cursor on every keystroke. Folded,
	// so the order reads the way the names do rather than putting every capital first.
	slices.SortFunc(out, func(a, b string) int {
		return strings.Compare(strings.ToLower(a), strings.ToLower(b))
	})
	return out
}

// lookup matches case-insensitively as a fallback because the name may be whatever a user typed
// into /play rather than a value the autocomplete handed back.
func (r *Registry) lookup(name string) (entry, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if e, ok := r.files[name]; ok {
		return e, true
	}
	for n, e := range r.files {
		if strings.EqualFold(n, name) || strings.EqualFold(Display(n), name) {
			return e, true
		}
	}
	return entry{}, false
}

// Path resolves a name to its file, rare or not.
func (r *Registry) Path(name string) (string, bool) {
	e, ok := r.lookup(name)
	return e.path, ok
}

// Playable reports whether name may be asked for by name, which a rare may not.
func (r *Registry) Playable(name string) bool {
	e, ok := r.lookup(name)
	return ok && !e.rare
}

// Pick draws a weighted sound and re-draws once when that repeats the last one: enough to make a
// repeat rare without skewing the weights the way excluding the last clip outright would.
func (r *Registry) Pick() (string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	name, ok := r.draw()
	if ok && name == r.last {
		name, ok = r.draw()
	}
	if ok {
		r.last = name
	}
	return name, ok
}

// draw walks the names in sorted order so a seeded intN gives the same sequence every run.
// Callers hold r.mu.
func (r *Registry) draw() (string, bool) {
	names := make([]string, 0, len(r.files))
	total := 0
	for n, e := range r.files {
		names = append(names, n)
		total += weight(e)
	}
	if total == 0 {
		return "", false
	}
	slices.Sort(names)
	x := r.intN(total)
	for _, n := range names {
		if x -= weight(r.files[n]); x < 0 {
			return n, true
		}
	}
	return "", false
}

func weight(e entry) int {
	if e.rare {
		return weightRare
	}
	return weightNormal
}

// Collection is how much of the live registry heard (distinct names) covers, per tier. A name no
// longer loaded counts for nothing, so deleting a file cannot leave anyone past the total.
type Collection struct {
	Got, Total, GotRare, TotalRare int
}

func (r *Registry) Collection(heard []string) Collection {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var c Collection
	for _, e := range r.files {
		if e.rare {
			c.TotalRare++
		} else {
			c.Total++
		}
	}
	for _, n := range heard {
		if e, ok := r.files[n]; ok && e.rare {
			c.GotRare++
		} else if ok {
			c.Got++
		}
	}
	return c
}

// PickOther is Pick without one name, for an encore that must not repeat the visit before it.
func (r *Registry) PickOther(name string) (string, bool) {
	names := slices.DeleteFunc(r.Names(), func(n string) bool { return n == name })
	if len(names) == 0 {
		return "", false
	}
	return names[rand.IntN(len(names))], true
}

func (r *Registry) Len() int { r.mu.RLock(); defer r.mu.RUnlock(); return len(r.files) }

// Start does a full scan, then watches with fsnotify (inotify). A 60 s rescan covers the
// mounts where inotify doesn't fire (NFS, SMB); set pollEvery > 0 to skip inotify entirely.
func (r *Registry) Start(ctx context.Context, pollEvery time.Duration) error {
	r.scan()
	time.Sleep(r.settle + 2*r.debounce) // let the initial batch validate before the loop's first tick

	if pollEvery > 0 {
		go r.pollLoop(ctx, pollEvery)
		slog.Info("sounds: polling", slog.String("dir", r.dir), slog.Duration("every", pollEvery), slog.Int("loaded", r.Len()))
		return nil
	}
	w, err := fsnotify.NewWatcher()
	if err != nil {
		slog.Warn("sounds: fsnotify unavailable, polling", slog.Any("err", err))
		go r.pollLoop(ctx, 10*time.Second)
		return nil
	}
	if err := w.Add(r.dir); err != nil {
		return err
	}
	go func() {
		defer func() {
			if err := w.Close(); err != nil {
				slog.Warn("sounds: closing watcher", slog.Any("err", err))
			}
		}()
		for {
			select {
			case <-ctx.Done():
				return
			case ev := <-w.Events:
				if name, _, ok := parse(filepath.Base(ev.Name)); ok {
					r.schedule(name)
				}
			case err := <-w.Errors:
				slog.Warn("sounds: watcher error", slog.Any("err", err))
			}
		}
	}()
	go r.pollLoop(ctx, 60*time.Second)
	slog.Info("sounds: watching", slog.String("dir", r.dir), slog.Int("loaded", r.Len()))
	return nil
}

func (r *Registry) pollLoop(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			r.scan()
		}
	}
}

func (r *Registry) scan() {
	entries, err := os.ReadDir(r.dir)
	if err != nil {
		return
	}
	want := r.wanted(entries)
	r.mu.RLock()
	var stale []string
	for name, path := range want {
		if r.files[name].path != path {
			stale = append(stale, name)
		}
	}
	r.mu.RUnlock()
	for _, name := range stale {
		r.schedule(name)
	}
	var gone []string
	r.mu.Lock()
	for name := range r.files {
		if _, ok := want[name]; !ok {
			delete(r.files, name)
			gone = append(gone, name)
		}
	}
	r.mu.Unlock()
	// Outside the lock: OnChange publishes, and every /play and autocomplete lookup waits on it.
	for _, name := range gone {
		r.notify(name, false)
	}
}

// wanted maps each name to the file that should back it: the rare one when both are there.
func (r *Registry) wanted(entries []os.DirEntry) map[string]string {
	want := map[string]string{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name, rare, ok := parse(e.Name())
		if !ok {
			continue
		}
		if _, dup := want[name]; !dup || rare {
			want[name] = filepath.Join(r.dir, e.Name())
		}
	}
	return want
}

// Debounce per file: editors and scp fire several events per write.
func (r *Registry) schedule(name string) {
	if v, ok := r.pending.Load(name); ok {
		if t, ok := v.(*time.Timer); ok {
			t.Stop()
		}
	}
	r.pending.Store(name, time.AfterFunc(r.debounce, func() {
		r.pending.Delete(name)
		r.consider(name)
	}))
}

// consider settles which file backs name, if any. It looks at both candidates on every call, so
// deleting foo.rare.ogg next to foo.ogg falls back to the normal one rather than dropping foo.
func (r *Registry) consider(name string) {
	rare := filepath.Join(r.dir, name+"."+tierRare+ext)
	normal := filepath.Join(r.dir, name+ext)
	// "foo.rare" names no normal file: foo.rare.ogg is the rare "foo".
	hasNormal := !strings.HasSuffix(name, "."+tierRare)
	var e entry
	var ok bool
	switch {
	case r.valid(rare):
		e, ok = entry{path: rare, rare: true}, true
		if _, err := os.Stat(normal); hasNormal && err == nil {
			slog.Warn("sounds: two files for one name, keeping the rare one",
				slog.String("name", name), slog.String("dropped", filepath.Base(normal)))
		}
	case hasNormal && r.valid(normal):
		e, ok = entry{path: normal}, true
	}
	r.mu.Lock()
	_, existed := r.files[name]
	if ok {
		r.files[name] = e
	} else {
		delete(r.files, name)
	}
	r.mu.Unlock()
	if ok != existed {
		r.notify(name, ok)
	}
}

func (r *Registry) valid(path string) bool { return settled(path, r.settle) && isOggOpus(path) }

func (r *Registry) notify(name string, added bool) {
	if r.OnChange != nil {
		r.OnChange(name, added)
	} else {
		slog.Info("sound changed", slog.String("name", name), slog.Bool("added", added))
	}
}

// Wait until the file stops growing — copies over the network arrive in chunks. The window is
// passed rather than read from a constant so a test can shorten it.
func settled(path string, window time.Duration) bool {
	a, err := os.Stat(path)
	if err != nil {
		return false
	}
	time.Sleep(window)
	b, err := os.Stat(path)
	return err == nil && a.Size() == b.Size() && b.Size() > 0
}

// "OggS" capture pattern + "OpusHead" in the first page. Rejects half-copied files and wrong codecs.
func isOggOpus(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }() //nolint:errcheck // read-only probe; a close error cannot change the verdict
	buf := make([]byte, 64)
	// A short file is a half-copied one: io.EOF here is a "not yet", not a failure.
	n, err := f.Read(buf)
	if err != nil && !errors.Is(err, io.EOF) {
		return false
	}
	return n >= 36 && bytes.HasPrefix(buf, []byte("OggS")) && bytes.Contains(buf[:n], []byte("OpusHead"))
}
