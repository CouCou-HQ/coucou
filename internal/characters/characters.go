// Package characters is every character a bot runs: each profile with the sounds it plays, and
// which one a server gets when it has not picked. One character is the bot as it always was.
package characters

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/be-sandaa/coucou/internal/profile"
	"github.com/be-sandaa/coucou/internal/sounds"
)

type Character struct {
	profile.Profile
	Sounds *sounds.Registry
}

// Set is safe to read while Reload swaps what it holds: every read sees one whole generation.
type Set struct {
	cur   atomic.Pointer[state]
	defID string

	mu       sync.Mutex // serialises Start and Reload
	ctx      context.Context
	poll     time.Duration
	onChange func(character string) func(name string, added bool)
	stop     map[*sounds.Registry]context.CancelFunc
}

type state struct {
	all  []*Character // sorted by id
	byID map[string]*Character
	def  *Character
}

// New pairs each profile with a registry over its sounds/. def names the default; empty is allowed
// only when there is one character, which is then the default.
func New(profiles []profile.Profile, def string) (*Set, error) {
	s := &Set{defID: def, stop: map[*sounds.Registry]context.CancelFunc{}}
	st, err := s.build(profiles, func(p profile.Profile) *sounds.Registry { return sounds.New(p.SoundsDir()) })
	if err != nil {
		return nil, err
	}
	s.cur.Store(st)
	return s, nil
}

// build arranges a generation over profiles, taking each registry from reg.
func (s *Set) build(profiles []profile.Profile, reg func(profile.Profile) *sounds.Registry) (*state, error) {
	st := &state{byID: make(map[string]*Character, len(profiles))}
	for _, p := range profiles {
		r := reg(p)
		r.Arrange(p.Chains, p.Links)
		c := &Character{Profile: p, Sounds: r}
		st.all = append(st.all, c)
		st.byID[p.ID] = c
	}
	switch {
	case len(st.all) == 0:
		return nil, errors.New("no characters")
	case s.defID == "" && len(st.all) > 1:
		return nil, fmt.Errorf("default_profile is required with %d characters", len(st.all))
	case s.defID == "":
		st.def = st.all[0]
	default:
		if st.def = st.byID[s.defID]; st.def == nil {
			return nil, fmt.Errorf("default_profile %q is not one of the characters", s.defID)
		}
	}
	return st, nil
}

// Get is the character with id, or the default for an empty or unknown one: a server that never
// picked, or whose character has since been removed.
func (s *Set) Get(id string) *Character {
	st := s.cur.Load()
	if c, ok := st.byID[id]; ok {
		return c
	}
	return st.def
}

func (s *Set) Default() *Character { return s.cur.Load().def }

func (s *Set) All() []*Character { return s.cur.Load().all }

// Len is how many sounds there are across every character.
func (s *Set) Len() int {
	n := 0
	for _, c := range s.All() {
		n += c.Sounds.Len()
	}
	return n
}

// Start watches every character's sounds/. onChange is built per character so a change says whose
// sound it was; it is set before Start so no registry reads it while it is written.
func (s *Set) Start(ctx context.Context, poll time.Duration, onChange func(character string) func(name string, added bool)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ctx, s.poll, s.onChange = ctx, poll, onChange
	for _, c := range s.All() {
		if err := s.start(c); err != nil {
			return err
		}
	}
	return nil
}

// start watches one character's sounds/ until Reload drops it or ctx ends. Callers hold mu.
func (s *Set) start(c *Character) error {
	ctx, cancel := context.WithCancel(s.ctx)
	c.Sounds.OnChange = s.onChange(c.ID)
	if err := c.Sounds.Start(ctx, s.poll); err != nil {
		cancel()
		return fmt.Errorf("%s: %w", c.ID, err)
	}
	s.stop[c.Sounds] = cancel
	return nil
}

// Reload swaps in profiles. A character whose directory is unchanged keeps its registry, so its
// sounds are not scanned and announced again; a new one is started, a gone one stopped. On an
// error nothing changes.
func (s *Set) Reload(profiles []profile.Profile) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	old := s.cur.Load()
	var fresh []*sounds.Registry
	st, err := s.build(profiles, func(p profile.Profile) *sounds.Registry {
		if c, ok := old.byID[p.ID]; ok && c.Dir == p.Dir {
			return c.Sounds
		}
		r := sounds.New(p.SoundsDir())
		fresh = append(fresh, r)
		return r
	})
	if err == nil {
		err = s.startFresh(st, fresh)
	}
	if err != nil {
		s.rollback(old, fresh)
		return err
	}
	for _, c := range old.all {
		if !slices.ContainsFunc(st.all, func(n *Character) bool { return n.Sounds == c.Sounds }) {
			s.halt(c.Sounds)
		}
	}
	s.cur.Store(st)
	return nil
}

// startFresh starts the registries Reload made, once Start has run. Callers hold mu.
func (s *Set) startFresh(st *state, fresh []*sounds.Registry) error {
	if s.ctx == nil {
		return nil
	}
	for _, c := range st.all {
		if slices.Contains(fresh, c.Sounds) {
			if err := s.start(c); err != nil {
				return err
			}
		}
	}
	return nil
}

// rollback undoes a Reload that failed: stops what it started and puts back the chains it
// re-arranged on the registries it kept. Callers hold mu.
func (s *Set) rollback(old *state, fresh []*sounds.Registry) {
	for _, r := range fresh {
		s.halt(r)
	}
	for _, c := range old.all {
		c.Sounds.Arrange(c.Chains, c.Links)
	}
}

func (s *Set) halt(r *sounds.Registry) {
	if cancel, ok := s.stop[r]; ok {
		cancel()
		delete(s.stop, r)
	}
}

// Marks is a sound's tag markers as whichever character has it shows them. Stats and boards span
// every character, and a name reads as the same sound to the people looking at them.
func (s *Set) Marks(name string) string {
	for _, c := range s.All() {
		if m := c.Sounds.Marks(name); m != "" {
			return m
		}
	}
	return ""
}

// Label is Registry.Label across every character.
func (s *Set) Label(name string) string { return sounds.Display(name) + s.Marks(name) }
