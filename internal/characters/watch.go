package characters

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"github.com/be-sandaa/coucou/internal/profile"
)

// A profile is checked every watchPoll and reloaded once it has stopped changing for watchQuiet:
// a profile is several files, and half an edit is a broken character. ponytail: polling rather
// than inotify, which also covers the NFS and SMB mounts inotify misses.
const (
	watchPoll  = 10 * time.Second
	watchQuiet = 30 * time.Second
)

// Watch reloads the set from load whenever the profile.toml or avatar.* files under dir change,
// then calls changed. A load or reload that fails is logged and the characters running stay.
func (s *Set) Watch(ctx context.Context, dir string, load func() ([]profile.Profile, error), changed func()) error {
	t := time.NewTicker(watchPoll)
	defer t.Stop()
	d := debounce{seen: fingerprint(dir)}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case now := <-t.C:
			if !d.settled(fingerprint(dir), now) {
				continue
			}
			if err := s.reloadFrom(load); err != nil {
				slog.Error("characters: reload refused, keeping the ones running", slog.Any("err", err))
				continue
			}
			slog.Info("characters: reloaded", slog.Int("characters", len(s.All())))
			changed()
		}
	}
}

// debounce holds a change back until it has stopped changing for watchQuiet.
type debounce struct {
	seen, pending string
	since         time.Time
}

// settled reports whether fp is a change from the last one acted on that has held for watchQuiet.
func (d *debounce) settled(fp string, now time.Time) bool {
	switch {
	case fp == d.seen:
		d.pending = ""
		return false
	case fp != d.pending:
		d.pending, d.since = fp, now
		return false
	case now.Sub(d.since) < watchQuiet:
		return false
	}
	d.seen, d.pending = fp, ""
	return true
}

func (s *Set) reloadFrom(load func() ([]profile.Profile, error)) error {
	ps, err := load()
	if err != nil {
		return err
	}
	return s.Reload(ps)
}

// fingerprint names every profile.toml and avatar.* under dir, one level of folders down, with
// its size and modification time. sounds/ is left out: the registries watch that themselves. A
// file that cannot be read is left out too, which is itself a change the next look sees.
func fingerprint(dir string) string {
	var sb strings.Builder
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return filepath.SkipDir
		case d.IsDir() && (d.Name() == "sounds" || (path != dir && filepath.Dir(path) != dir)):
			return filepath.SkipDir
		case d.IsDir(), d.Name() != "profile.toml" && !strings.HasPrefix(d.Name(), "avatar."):
			return nil
		}
		if info, ierr := d.Info(); ierr == nil {
			fmt.Fprintf(&sb, "%s %d %d\n", path, info.Size(), info.ModTime().UnixNano())
		}
		return nil
	})
	if err != nil {
		return "" // unreadable is a state of its own; reading it again later is a change
	}
	return sb.String()
}
