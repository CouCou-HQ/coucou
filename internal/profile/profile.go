// Package profile reads the character a bot running on coucou plays: its profile.toml, and the
// sounds directory beside it. coucou itself is never a character; everything that makes one bot
// different from another lives here.
package profile

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/BurntSushi/toml"
	"github.com/disgoorg/disgo/discord"

	"github.com/be-sandaa/coucou/internal/store"
)

// The ranges every guild setting is held to, shared with the slash commands that change them and
// matching the schema's check constraints.
const (
	MaxChance   = 100
	MaxSuspense = 20
	MaxFakeOut  = 50
	MaxEncore   = 50
	MaxStatus   = 128 // characters of status text, Discord's custom status limit
)

// defaultChance is the join chance a guild starts with when the profile does not say, as a
// percentage rolled once every 5 minutes — and only on ticks where somebody is actually sitting in
// a voice channel.
//
// At 5%, a guild with people in voice sees roughly one visit per 1.5-2 hours of that voice time:
// enough that the bot visibly does something the day it is added, not so much that a four-hour
// session gets interrupted five times. 0 would leave a freshly added bot looking broken, and 10+
// is how a joke bot gets removed. Guilds tune it per server with /chance.
const defaultChance = 5

// DefaultColor is the embed accent when the profile names none: the brand hue in DESIGN.md.
const DefaultColor = 0xE4572E

type Profile struct {
	Dir      string
	ID       string
	Nickname string // empty: the bot goes by its server nickname, else its Discord name
	Emoji    string
	Color    int
	Tagline  string
	Lore     string
	Traits   []string
	Defaults store.Defaults
	Status   Status
}

// Status is what the bot shows under its name in the member list. No Text: only the online state.
type Status struct {
	Text     string
	Activity discord.ActivityType
	Online   discord.OnlineStatus
}

const defaultActivity, defaultOnline = "custom", "online"

// Streaming is left out: it needs a Twitch or YouTube URL to show as streaming.
var (
	activities = map[string]discord.ActivityType{
		defaultActivity: discord.ActivityTypeCustom, "playing": discord.ActivityTypeGame,
		"listening": discord.ActivityTypeListening, "watching": discord.ActivityTypeWatching,
		"competing": discord.ActivityTypeCompeting,
	}
	onlines = map[string]discord.OnlineStatus{
		defaultOnline: discord.OnlineStatusOnline, "idle": discord.OnlineStatusIdle, "dnd": discord.OnlineStatusDND,
	}
)

// SoundsDir is where the character's sounds live: always beside its profile.toml.
func (p Profile) SoundsDir() string { return filepath.Join(p.Dir, "sounds") }

// file is profile.toml as written.
type file struct {
	ID       string   `toml:"id"`
	Nickname string   `toml:"nickname"`
	Emoji    string   `toml:"emoji"`
	Color    string   `toml:"color"`
	Tagline  string   `toml:"tagline"`
	Lore     string   `toml:"lore"`
	Traits   []string `toml:"traits"`
	Defaults struct {
		Chance   int `toml:"chance"`
		Suspense int `toml:"suspense"`
		FakeOut  int `toml:"fakeout"`
		Encore   int `toml:"encore"`
	} `toml:"defaults"`
	Status struct {
		Text     string `toml:"text"`
		Activity string `toml:"activity"`
		Online   string `toml:"online"`
	} `toml:"status"`
}

var idPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// Load reads dir/profile.toml. It is read once, at startup: a changed profile needs a restart, and
// only the sounds beside it reload live.
func Load(dir string) (Profile, error) { return load(filepath.Join(dir, "profile.toml"), dir) }

func load(path, dir string) (Profile, error) {
	var f file
	f.Defaults.Chance = defaultChance
	f.Status.Activity, f.Status.Online = defaultActivity, defaultOnline
	md, err := toml.DecodeFile(path, &f)
	if err != nil {
		return Profile{}, err
	}
	if extra := md.Undecoded(); len(extra) > 0 {
		keys := make([]string, len(extra))
		for i, k := range extra {
			keys[i] = k.String()
		}
		return Profile{}, fmt.Errorf("%s: unknown keys %s", path, strings.Join(keys, ", "))
	}
	p, err := f.parse(dir)
	if err != nil {
		return Profile{}, fmt.Errorf("%s: %w", path, err)
	}
	return p, nil
}

func (f file) parse(dir string) (Profile, error) {
	if !idPattern.MatchString(f.ID) {
		return Profile{}, fmt.Errorf("id %q: want lowercase letters, digits and dashes", f.ID)
	}
	color, err := parseColor(f.Color)
	if err != nil {
		return Profile{}, err
	}
	d := f.Defaults
	if err := errors.Join(
		inRange("defaults.chance", d.Chance, MaxChance),
		inRange("defaults.suspense", d.Suspense, MaxSuspense),
		inRange("defaults.fakeout", d.FakeOut, MaxFakeOut),
		inRange("defaults.encore", d.Encore, MaxEncore),
	); err != nil {
		return Profile{}, err
	}
	status, err := f.status()
	if err != nil {
		return Profile{}, err
	}
	return Profile{
		Dir: dir, ID: f.ID, Nickname: strings.TrimSpace(f.Nickname), Emoji: f.Emoji, Color: color,
		Tagline: f.Tagline, Lore: strings.TrimSpace(f.Lore), Traits: f.Traits,
		Defaults: store.Defaults{Chance: d.Chance, Suspense: d.Suspense, FakeOut: d.FakeOut, Encore: d.Encore},
		Status:   status,
	}, nil
}

func (f file) status() (Status, error) {
	st := f.Status
	text := strings.TrimSpace(st.Text)
	activity, ok := activities[st.Activity]
	if !ok {
		return Status{}, fmt.Errorf("status.activity %q: want custom, playing, listening, watching or competing", st.Activity)
	}
	online, ok := onlines[st.Online]
	if !ok {
		return Status{}, fmt.Errorf("status.online %q: want online, idle or dnd", st.Online)
	}
	if n := utf8.RuneCountInString(text); n > MaxStatus {
		return Status{}, fmt.Errorf("status.text is %d characters, over %d", n, MaxStatus)
	}
	return Status{Text: text, Activity: activity, Online: online}, nil
}

// parseColor reads "#RRGGBB". Empty is the brand hue, not black: an unset accent should look like
// every other coucou embed, not like a mistake.
func parseColor(s string) (int, error) {
	if s == "" {
		return DefaultColor, nil
	}
	if len(s) != 7 || s[0] != '#' {
		return 0, fmt.Errorf("color %q: want #RRGGBB", s)
	}
	n, err := strconv.ParseUint(s[1:], 16, 32)
	if err != nil {
		return 0, fmt.Errorf("color %q: want #RRGGBB", s)
	}
	return int(n), nil
}

func inRange(key string, n, maxN int) error {
	if n < 0 || n > maxN {
		return fmt.Errorf("%s %d is outside 0-%d", key, n, maxN)
	}
	return nil
}
