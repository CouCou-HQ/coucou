package app

import (
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/disgoorg/snowflake/v2"

	"github.com/be-sandaa/coucou/internal/commands"
)

// defaultChance is the join chance a guild starts with, as a percentage rolled once every 5 minutes
// — and only on ticks where somebody is actually sitting in a voice channel.
//
// At 5%, a guild with people in voice sees roughly one visit per 1.5-2 hours of that voice time:
// enough that the bot visibly does something the day it is added, not so much that a four-hour
// session gets interrupted five times. 0 would leave a freshly added bot looking broken, and 10+
// is how a joke bot gets removed. Guilds tune it per server with /chance.
const defaultChance = 5

// config is every knob the bot has. Each one is a CLI flag with the matching environment variable as
// its fallback, so a flag wins when given — which suits the container (env in the unit file) and
// a shell session alike (a flag for one run).
type config struct {
	DatabaseURL   string
	Token         string
	OwnerIDs      []snowflake.ID
	Siblings      []commands.Sibling
	SoundsDir     string
	SoundsPoll    time.Duration
	HTTPAddr      string
	OTLPEndpoint  string
	LogLevel      slog.Level
	PProf         bool
	DefaultChance int16
	ShardCount    int
}

// parseConfig builds the configuration from args, falling back to the environment for anything not
// given as a flag. args excludes the program name, as os.Args[1:] does.
func parseConfig(args []string) (config, error) {
	fs := flag.NewFlagSet("coucou", flag.ContinueOnError)
	fs.Usage = func() {
		if _, err := fmt.Fprint(fs.Output(), "Usage: coucou [flags]\n\n"+
			"Every flag falls back to the environment variable named in its description.\n\n"); err != nil {
			return // the usage stream is gone; PrintDefaults would only fail too
		}
		fs.PrintDefaults()
	}

	f := declareFlags(fs)

	if err := fs.Parse(args); err != nil {
		return config{}, err
	}
	if fs.NArg() > 0 {
		return config{}, fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}

	// A flag the caller actually passed wins; anything else falls back to the environment.
	f.applyEnv(fs)

	c := config{
		DatabaseURL:  *f.databaseURL,
		Token:        *f.token,
		SoundsDir:    *f.soundsDir,
		HTTPAddr:     *f.httpAddr,
		OTLPEndpoint: *f.otlp,
	}

	var err error
	if c.SoundsPoll, err = time.ParseDuration(*f.soundsPoll); err != nil {
		return config{}, fmt.Errorf("sounds-poll %q: %w", *f.soundsPoll, err)
	}
	if c.DefaultChance, err = parseChance(*f.chance); err != nil {
		return config{}, err
	}
	if c.OwnerIDs, c.Siblings, err = parseLists(f); err != nil {
		return config{}, err
	}
	if c.ShardCount, err = parseShardCount(*f.shardCount); err != nil {
		return config{}, err
	}
	if c.LogLevel, err = parseLogLevel(*f.logLevel); err != nil {
		return config{}, err
	}
	if c.PProf, err = parsePProf(*f.pprof); err != nil {
		return config{}, err
	}
	return c, c.validate()
}

// rawFlags is every flag as the caller gave it — strings, before any parsing. Declaring them is
// split out of parseConfig because it is a list, not logic, and the two together outgrew funlen.
type rawFlags struct {
	databaseURL, token, ownerIDs    *string
	soundsDir, soundsPoll, chance   *string
	httpAddr, logLevel, pprof, otlp *string
	shardCount, siblings            *string
}

// declareFlags registers every flag on fs. Call before fs.Parse.
func declareFlags(fs *flag.FlagSet) rawFlags {
	// Flags default to empty, never to the environment value: flag.PrintDefaults echoes defaults,
	// so a -h with DISCORD_BOT_TOKEN or a DSN password set would print the secret. The environment
	// is applied below instead, only for flags the caller did not pass.
	var (
		databaseURL = fs.String("database-url", "", "PostgreSQL or SQLite connection string; the scheme picks the backend [DATABASE_URL]")
		token       = fs.String("discord-token", "", "Discord bot token [DISCORD_BOT_TOKEN]")
		ownerIDs    = fs.String("owner-ids", "", "comma-separated user ids that unlock the servers leaderboard [OWNER_IDS]")
		soundsDir   = fs.String("sounds-dir", "sounds", "directory of pre-encoded Ogg Opus files [SOUNDS_DIR]")
		soundsPoll  = fs.String("sounds-poll", "0s", "rescan interval; 0s uses inotify [SOUNDS_POLL]")
		chance      = fs.String("default-chance", strconv.Itoa(defaultChance), "join chance seeded for guilds with no settings row, 0-100 [DEFAULT_CHANCE]")
		httpAddr    = fs.String("http-addr", ":9090", "listen address for /healthz, /readyz and /metrics; empty disables them [HTTP_ADDR]")
		logLevel    = fs.String("log-level", "info", "console log level: debug | info | warn | error [LOG_LEVEL]")
		pprof       = fs.String("pprof", "false", "serve /debug/pprof on the ops listener; -pprof=true, off by default [PPROF]")
		otlp        = fs.String("otlp-endpoint", "", "host:port of an OTLP/gRPC collector; empty disables tracing [OTLP_ENDPOINT]")
		shardCount  = fs.String("shard-count", "0", "shards to run; 0 lets Discord decide, which is what a deploy should do [SHARD_COUNT]")
		siblings    = fs.String("siblings", "", "comma-separated Name=application-id pairs of other coucou bots to list in /help; this bot is left out [SIBLINGS]")
	)

	return rawFlags{
		databaseURL: databaseURL, token: token, ownerIDs: ownerIDs,
		soundsDir: soundsDir, soundsPoll: soundsPoll, chance: chance,
		httpAddr: httpAddr, logLevel: logLevel, pprof: pprof, otlp: otlp,
		shardCount: shardCount, siblings: siblings,
	}
}

// applyEnv fills each flag the caller did not pass from its environment variable.
func (f rawFlags) applyEnv(fs *flag.FlagSet) {
	fallback := envFallback(fs)
	fallback("database-url", "DATABASE_URL", f.databaseURL)
	fallback("discord-token", "DISCORD_BOT_TOKEN", f.token)
	fallback("owner-ids", "OWNER_IDS", f.ownerIDs)
	fallback("sounds-dir", "SOUNDS_DIR", f.soundsDir)
	fallback("sounds-poll", "SOUNDS_POLL", f.soundsPoll)
	fallback("default-chance", "DEFAULT_CHANCE", f.chance)
	fallback("http-addr", "HTTP_ADDR", f.httpAddr)
	fallback("otlp-endpoint", "OTLP_ENDPOINT", f.otlp)
	fallback("log-level", "LOG_LEVEL", f.logLevel)
	fallback("pprof", "PPROF", f.pprof)
	fallback("shard-count", "SHARD_COUNT", f.shardCount)
	fallback("siblings", "SIBLINGS", f.siblings)
}

// fallbackFunc fills one flag from its environment variable when the caller did not pass it.
type fallbackFunc = func(name, env string, target *string)

// envFallback returns the function that fills one flag from its environment variable. The set of
// flags the caller actually passed is read once, here, rather than on every lookup.
func envFallback(fs *flag.FlagSet) fallbackFunc {
	passed := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { passed[f.Name] = true })
	return func(name, env string, target *string) {
		if passed[name] {
			return
		}
		if v := os.Getenv(env); v != "" {
			*target = v
		}
	}
}

// validate checks what a run needs before it can start: somewhere to store state, and a token to
// reach Discord with.
func (c config) validate() error {
	if c.DatabaseURL == "" {
		return errors.New("missing -database-url (or DATABASE_URL)")
	}
	if c.Token == "" {
		return errors.New("missing -discord-token (or DISCORD_BOT_TOKEN)")
	}
	return nil
}

// parseChance reads the default join chance, which the schema constrains to 0-100.
func parseChance(v string) (int16, error) {
	if v == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("default-chance %q: %w", v, err)
	}
	if n < 0 || n > 100 {
		return 0, fmt.Errorf("default-chance %d is outside 0-100", n)
	}
	return int16(n), nil //nolint:gosec // G109: bounded to 0-100 immediately above
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
		return 0, fmt.Errorf("log-level %q: %w", v, err)
	}
	return l, nil
}

// parsePProf reads the profiling toggle. A string flag rather than fs.Bool so it goes through the
// same environment fallback as everything else, at the cost of needing -pprof=true spelled out.
func parsePProf(v string) (bool, error) {
	if v == "" {
		return false, nil
	}
	on, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("pprof %q: %w", v, err)
	}
	return on, nil
}

// parseShardCount reads the shard override. 0 is the normal answer: Discord computes the count and
// disgo opens that many. A value above that exists to force more shards than the bot's size earns,
// which is the only way to exercise the multi-shard paths before it is big enough to be given them.
func parseShardCount(v string) (int, error) {
	if v == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("shard-count %q: %w", v, err)
	}
	if n < 0 {
		return 0, fmt.Errorf("shard-count %d is negative", n)
	}
	return n, nil
}

// parseOwners reads the owner list. Unset is fine — it only gates the servers leaderboard, and a
// bot with no owner simply has none.
func parseOwners(v string) ([]snowflake.ID, error) {
	parts := splitList(v)
	if len(parts) == 0 {
		return nil, nil
	}
	ids := make([]snowflake.ID, 0, len(parts))
	for _, p := range parts {
		id, err := snowflake.Parse(p)
		if err != nil {
			return nil, fmt.Errorf("owner-ids %q: %w", p, err)
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// parseLists reads the two id lists, split out of parseConfig to keep it under gocyclo's bar.
func parseLists(f rawFlags) ([]snowflake.ID, []commands.Sibling, error) {
	owners, err := parseOwners(*f.ownerIDs)
	if err != nil {
		return nil, nil, err
	}
	siblings, err := parseSiblings(*f.siblings)
	if err != nil {
		return nil, nil, err
	}
	return owners, siblings, nil
}

// parseSiblings reads the other bots /help advertises: Name=application id, comma separated, so a
// name may hold spaces ("The Narrator") but not a comma.
func parseSiblings(v string) ([]commands.Sibling, error) {
	parts := splitList(v)
	out := make([]commands.Sibling, 0, len(parts))
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

// splitList turns a comma-separated flag into a slice, dropping blanks so an unset value is nil
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
