package app

import (
	"cmp"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"reflect"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/BurntSushi/toml"
	"github.com/disgoorg/snowflake/v2"

	"github.com/be-sandaa/coucou/internal/commands"
)

const (
	defaultConfigPath = "/etc/coucou/config.toml"
	defaultProfileDir = "/var/lib/coucou/profile"
	defaultHTTPAddr   = ":9090"
)

// config is every knob the bot has, read from one TOML file. Secrets reach it as ${VAR}
// references inside string values rather than through a second way of setting the same key.
type config struct {
	DatabaseURL    string
	Token          string
	OwnerIDs       []snowflake.ID
	Siblings       []commands.Sibling
	ProfileDir     string // one character; deprecated, empty when ProfilesDir is set
	ProfilesDir    string // one directory per character
	DefaultProfile string
	Note           string
	SoundsPoll     time.Duration
	HTTPAddr       string
	OTLPEndpoint   string
	LogLevel       slog.Level
	PProf          bool
	ShardCount     int
}

// file is config.toml as written, before parsing. Every string field may hold ${VAR} references;
// the non-string ones cannot, because they are typed by the decoder before expansion runs.
type file struct {
	DiscordToken   string   `toml:"discord_token"`
	DatabaseURL    string   `toml:"database_url"`
	OwnerIDs       []string `toml:"owner_ids"`
	Siblings       string   `toml:"siblings"`
	Profile        string   `toml:"profile"`
	Profiles       string   `toml:"profiles"`
	DefaultProfile string   `toml:"default_profile"`
	Note           string   `toml:"note"`
	Sounds         struct {
		Poll time.Duration `toml:"poll"`
	} `toml:"sounds"`
	Ops struct {
		HTTPAddr string `toml:"http_addr"`
		LogLevel string `toml:"log_level"`
		PProf    bool   `toml:"pprof"`
		OTLP     string `toml:"otlp"`
	} `toml:"ops"`
	Gateway struct {
		ShardCount int `toml:"shard_count"`
	} `toml:"gateway"`
}

// parseConfig reads the file named by -config, else COUCOU_CONFIG, else /etc/coucou/config.toml.
// There is deliberately no ./config.toml fallback: a service must not pick up whatever file sits in
// the directory it happened to start in. args excludes the program name, as os.Args[1:] does.
func parseConfig(args []string) (config, error) {
	fs := flag.NewFlagSet("coucou", flag.ContinueOnError)
	path := fs.String("config", "", "path to the TOML config file [COUCOU_CONFIG] (default "+defaultConfigPath+")")
	if err := fs.Parse(args); err != nil {
		return config{}, err
	}
	if fs.NArg() > 0 {
		return config{}, fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	if *path == "" {
		*path = cmp.Or(os.Getenv("COUCOU_CONFIG"), defaultConfigPath)
	}

	f, legacy, err := readFile(*path)
	if err != nil {
		return config{}, err
	}
	if legacy {
		slog.Warn("config: profile is deprecated and will be removed; move the directory to profiles/<id>/ and set profiles", slog.String("profile", f.Profile))
	}
	c, err := f.parse()
	if err != nil {
		return config{}, fmt.Errorf("%s: %w", *path, err)
	}
	return c, nil
}

// readFile decodes path over the defaults, refuses keys the file has that config does not, and
// then expands ${VAR} references in the decoded strings. legacy is whether the file names the
// deprecated profile key; the default it falls back to says nothing about the operator's intent.
func readFile(path string) (f file, legacy bool, err error) {
	f.Ops.HTTPAddr = defaultHTTPAddr

	md, err := toml.DecodeFile(path, &f)
	if err != nil {
		return file{}, false, err
	}
	if extra := md.Undecoded(); len(extra) > 0 {
		keys := make([]string, len(extra))
		for i, k := range extra {
			keys[i] = k.String()
		}
		return file{}, false, fmt.Errorf("%s: unknown keys %s", path, strings.Join(keys, ", "))
	}
	if err := expandEnv(reflect.ValueOf(&f).Elem()); err != nil {
		return file{}, false, fmt.Errorf("%s: %w", path, err)
	}
	switch {
	case f.Profile != "" && f.Profiles != "":
		return file{}, false, fmt.Errorf("%s: set profiles or the deprecated profile, not both", path)
	case f.Profile == "" && f.Profiles == "":
		f.Profile = defaultProfileDir
	}
	return f, md.IsDefined("profile"), nil
}

// envRef is ${NAME} or ${NAME:-default}. A bare $NAME is deliberately not a reference, so a
// literal $ in a password or DSN survives.
var envRef = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(?::-([^}]*))?\}`)

// expandEnv replaces every ${VAR} in the strings under v. It runs after decoding, not over the raw
// file, so a substituted value is never parsed as TOML: a secret holding a quote or a backslash
// cannot break the file, and a reference inside a comment is never looked up.
func expandEnv(v reflect.Value) error {
	switch v.Kind() {
	case reflect.String:
		s, err := expand(v.String())
		v.SetString(s)
		return err
	case reflect.Struct:
		for i := range v.NumField() {
			if err := expandEnv(v.Field(i)); err != nil {
				return err
			}
		}
	case reflect.Slice:
		for i := range v.Len() {
			if err := expandEnv(v.Index(i)); err != nil {
				return err
			}
		}
	default:
	}
	return nil
}

// expand follows the shell: ${VAR:-default} takes the default when VAR is unset or empty, and a
// plain ${VAR} that is unset is an error rather than a quiet empty token.
func expand(s string) (string, error) {
	var missing []string
	out := envRef.ReplaceAllStringFunc(s, func(ref string) string {
		m := envRef.FindStringSubmatch(ref)
		v, set := os.LookupEnv(m[1])
		if strings.Contains(ref, ":-") {
			return cmp.Or(v, m[2])
		}
		if !set {
			missing = append(missing, m[1])
		}
		return v
	})
	if len(missing) > 0 {
		return "", fmt.Errorf("environment variable %s is not set", strings.Join(missing, ", "))
	}
	return out, nil
}

// parse turns the file into the config the rest of the program reads, checking every value.
func (f file) parse() (config, error) {
	c := config{
		DatabaseURL:    f.DatabaseURL,
		Token:          f.DiscordToken,
		ProfileDir:     f.Profile,
		ProfilesDir:    f.Profiles,
		DefaultProfile: strings.TrimSpace(f.DefaultProfile),
		Note:           strings.TrimSpace(f.Note),
		SoundsPoll:     f.Sounds.Poll,
		HTTPAddr:       f.Ops.HTTPAddr,
		OTLPEndpoint:   f.Ops.OTLP,
		PProf:          f.Ops.PProf,
	}
	var err error
	if c.OwnerIDs, err = parseOwners(f.OwnerIDs); err != nil {
		return config{}, err
	}
	if c.Siblings, err = parseSiblings(f.Siblings); err != nil {
		return config{}, err
	}
	if c.ShardCount, err = parseShardCount(f.Gateway.ShardCount); err != nil {
		return config{}, err
	}
	if c.LogLevel, err = parseLogLevel(f.Ops.LogLevel); err != nil {
		return config{}, err
	}
	return c, c.validate()
}

// maxNote is Discord's cap on an embed field, which keeps the note from crowding /about's own
// limit out of the character sheet it is appended to.
const maxNote = 1024

// validate checks what a run needs before it can start: somewhere to store state, and a token to
// reach Discord with.
func (c config) validate() error {
	if c.DatabaseURL == "" {
		return errors.New("missing database_url")
	}
	if n := utf8.RuneCountInString(c.Note); n > maxNote {
		return fmt.Errorf("note is %d characters, over %d", n, maxNote)
	}
	if c.Token == "" {
		return errors.New("missing discord_token")
	}
	return nil
}

// parseLogLevel reads the console level. slog.Level is a TextUnmarshaler, so it already accepts
// DEBUG/INFO/WARN/ERROR in any case and offsets such as INFO+2 — there is no parsing to write.
// It is the console only: an audit record is stored whatever this says, because the row is written
// before the line is logged.
func parseLogLevel(v string) (slog.Level, error) {
	if v == "" {
		return slog.LevelInfo, nil
	}
	var l slog.Level
	if err := l.UnmarshalText([]byte(v)); err != nil {
		return 0, fmt.Errorf("log_level %q: %w", v, err)
	}
	return l, nil
}

// parseShardCount reads the shard override. 0 is the normal answer: Discord computes the count and
// disgo opens that many. A value above that exists to force more shards than the bot's size earns,
// which is the only way to exercise the multi-shard paths before it is big enough to be given them.
func parseShardCount(n int) (int, error) {
	if n < 0 {
		return 0, fmt.Errorf("shard_count %d is negative", n)
	}
	return n, nil
}

// parseOwners reads the owner list. Unset is fine — it only gates the servers leaderboard, and a
// bot with no owner simply has none.
func parseOwners(v []string) ([]snowflake.ID, error) {
	var ids []snowflake.ID
	for _, p := range v {
		id, err := snowflake.Parse(strings.TrimSpace(p))
		if err != nil {
			return nil, fmt.Errorf("owner_ids %q: %w", p, err)
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// parseSiblings reads the other bots /help advertises: Name=application id, comma separated, so a
// name may hold spaces ("The Narrator") but not a comma.
func parseSiblings(v string) ([]commands.Sibling, error) {
	parts := splitList(v)
	var out []commands.Sibling
	for _, p := range parts {
		name, id, ok := strings.Cut(p, "=")
		name = strings.TrimSpace(name)
		if !ok || name == "" {
			return nil, fmt.Errorf("siblings %q: want Name=application-id", p)
		}
		app, err := snowflake.Parse(strings.TrimSpace(id))
		if err != nil {
			return nil, fmt.Errorf("siblings %q: %w", p, err)
		}
		out = append(out, commands.Sibling{Name: name, App: app})
	}
	return out, nil
}

// splitList turns a comma-separated value into a slice, dropping blanks so an unset value is nil
// rather than a one-element slice holding "".
func splitList(v string) []string {
	var out []string
	for _, part := range strings.Split(v, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}
