// Package characters is every character a bot runs: each profile with the sounds it plays, and
// which one a server gets when it has not picked. One character is the bot as it always was.
package characters

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/be-sandaa/coucou/internal/profile"
	"github.com/be-sandaa/coucou/internal/sounds"
)

type Character struct {
	profile.Profile
	Sounds *sounds.Registry
}

type Set struct {
	all  []*Character // sorted by id
	byID map[string]*Character
	def  *Character
}

// New pairs each profile with a registry over its sounds/. def names the default; empty is allowed
// only when there is one character, which is then the default.
func New(profiles []profile.Profile, def string) (*Set, error) {
	s := &Set{byID: make(map[string]*Character, len(profiles))}
	for _, p := range profiles {
		reg := sounds.New(p.SoundsDir())
		reg.Arrange(p.Chains, p.Links)
		c := &Character{Profile: p, Sounds: reg}
		s.all = append(s.all, c)
		s.byID[p.ID] = c
	}
	switch {
	case len(s.all) == 0:
		return nil, errors.New("no characters")
	case def == "" && len(s.all) > 1:
		return nil, fmt.Errorf("default_profile is required with %d characters", len(s.all))
	case def == "":
		s.def = s.all[0]
	default:
		if s.def = s.byID[def]; s.def == nil {
			return nil, fmt.Errorf("default_profile %q is not one of the characters", def)
		}
	}
	return s, nil
}

// Get is the character with id, or the default for an empty or unknown one: a server that never
// picked, or whose character has since been removed.
func (s *Set) Get(id string) *Character {
	if c, ok := s.byID[id]; ok {
		return c
	}
	return s.def
}

func (s *Set) Default() *Character { return s.def }

func (s *Set) All() []*Character { return s.all }

// Len is how many sounds there are across every character.
func (s *Set) Len() int {
	n := 0
	for _, c := range s.all {
		n += c.Sounds.Len()
	}
	return n
}

// Start watches every character's sounds/. onChange is built per character so a change says whose
// sound it was; it is set before Start so no registry reads it while it is written.
func (s *Set) Start(ctx context.Context, poll time.Duration, onChange func(character string) func(name string, added bool)) error {
	for _, c := range s.all {
		c.Sounds.OnChange = onChange(c.ID)
		if err := c.Sounds.Start(ctx, poll); err != nil {
			return fmt.Errorf("%s: %w", c.ID, err)
		}
	}
	return nil
}

// Marks is a sound's tag markers as whichever character has it shows them. Stats and boards span
// every character, and a name reads as the same sound to the people looking at them.
func (s *Set) Marks(name string) string {
	for _, c := range s.all {
		if m := c.Sounds.Marks(name); m != "" {
			return m
		}
	}
	return ""
}

// Label is Registry.Label across every character.
func (s *Set) Label(name string) string { return sounds.Display(name) + s.Marks(name) }
