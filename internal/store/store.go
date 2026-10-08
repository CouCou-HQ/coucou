// Package store abstracts the database behind one interface and picks an implementation from the
// DATABASE_URL scheme:
//
//	postgres://…  postgresql://…   → store/pg     (pgx, sqlc postgresql engine)
//	sqlite://path  file:path        → store/sqlite (modernc.org/sqlite, pure Go; sqlc sqlite engine)
//
// Each backend owns its migrations (goose, per-dialect) and its generated queries (sqlc, per-engine).
// The interface is the contract; the SQL is allowed to differ completely.
package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/disgoorg/snowflake/v2"
)

// UpdatedBy is who asked for the change, written so the audit trigger can copy it into the record.
// Zero is the bot acting on its own — seeding a guild it just joined, reconciling at boot. It is
// write-only: ListSettings leaves it zero, because the current settings are a state and the actor
// belongs to the change that produced it. Backends without the audit triggers ignore it.
type Settings struct {
	Guild     snowflake.ID
	Chance    int
	TZ        *string
	Suspense  int
	FakeOut   int
	Encore    int
	NSFW      string
	Character string // a profile id; empty is the default character, stored as null
	UpdatedBy snowflake.ID
}

// Defaults is what a guild's settings row starts as, from the bot's profile. Seeding never touches a
// row that already exists, so changing them only reaches guilds that have none yet.
type Defaults struct {
	Chance   int
	Suspense int
	FakeOut  int
	Encore   int
}

type Play struct {
	At          time.Time
	Guild       snowflake.ID
	Channel     snowflake.ID
	Sound       string
	Trigger     string
	User        *snowflake.ID
	ListenerIDs []snowflake.ID
	FledIDs     []snowflake.ID // a subset of ListenerIDs
	OK          bool
	Reason      string
	Duration    time.Duration
	Character   string // empty for a play recorded before characters existed
}

// Misc is one row of the append-only events table. Guild is nil for a global event — a sound
// appearing or disappearing belongs to no server.
type Misc struct {
	At    time.Time
	Kind  string
	Guild *snowflake.ID
	User  *snowflake.ID
	Data  []byte // JSON
}

// Guild is presence, not profile. Name and member count used to live here too; they were written
// on every reconcile, read by nothing, and stale in between — the gateway cache has the live ones.
type Guild struct {
	ID       snowflake.ID
	JoinedAt time.Time
}

type GuildStats struct {
	PlaysAll, Plays7d int
	FailRate7d        *float64
	TopSound          *string
	LoudestHour       *int
	AvgListeners      *float64
}

// UserStats is one person's side of a guild's numbers: how often they were in the room when the
// bot turned up, how often they asked for it themselves, how often they fled it, and when they
// were last caught.
type UserStats struct {
	Heard, Triggered, Fled int
	LastHeard              *time.Time
}

type GlobalStats struct {
	Plays24h, Guilds24h int
	TopSound            *string
}

// The metrics Cuts ranks a population on.
const (
	MetricHeard     = "heard"
	MetricTriggered = "triggered"
	MetricFled      = "fled"
	MetricPlays     = "plays"
	MetricListeners = "listeners"
)

// UserCounts is one person's numbers inside a rank's window.
type UserCounts struct {
	Heard, Triggered, Fled int
}

// UserRank is UserCounts in one guild, with how many of its people sit strictly below each. Of is
// that population, everyone caught there in the window; zero when the user is not among them.
type UserRank struct {
	UserCounts
	HeardBelow, TriggeredBelow, FledBelow, Of int
}

// GuildRecent is a guild's side of the guild ranks, on ok plays inside the window.
type GuildRecent struct {
	Plays        int
	AvgListeners float64
}

// Silence is one live row of a person's /optout or a guild's /quiet. No Rule is all the time; a
// Rule is its occurrences, each Window long. Until ends either shape, nil runs until turned off.
//
// Rule is an RFC 5545 rule with its own DTSTART and TZID line, so it carries the time zone it is
// read in. Window is how long each occurrence lasts — a recurrence rule yields instants, and a
// silence is an interval. By is write-only, like Settings.UpdatedBy, and opt-outs have none.
type Silence struct {
	ID     snowflake.ID // the user for an opt-out, the guild for quiet
	Rule   string
	Window time.Duration
	Until  *time.Time
	By     snowflake.ID
}

// Chaos is a guild's /chaos window: a weekly Rule, carrying its zone as Silence.Rule does, whose
// occurrences each last Hours, and the Chance the loop rolls against inside one. An empty Rule is
// /chaos off. CreatedBy is write-only, like Settings.UpdatedBy.
type Chaos struct {
	Guild     snowflake.ID
	Rule      string
	Hours     int
	Chance    int
	CreatedBy snowflake.ID
}

// PlayHour is one UTC hour of plays, in a guild or across the bot. Plays is the ok ones, split by
// trigger into Loops, Commands and Encores; FakeOuts and Failed are the two ways a visit comes to
// nothing, kept apart because a fake-out is the bot working as asked. Listeners is summed over the
// ok plays, and Guilds is how many servers had one — only meaningful bot-wide.
type PlayHour struct {
	Hour                     time.Time
	Plays, Failed, FakeOuts  int
	Loops, Commands, Encores int
	Listeners, Guilds        int
}

// UserHour is one UTC hour of a person's UserStats counts.
type UserHour struct {
	Hour                   time.Time
	Heard, Fled, Triggered int
}

type Row struct {
	Key string
	N   int
}

type Store interface {
	// lifecycle
	Migrate(ctx context.Context) error
	Ping(ctx context.Context) error
	Close()

	// settings
	ListSettings(ctx context.Context) ([]Settings, error)
	UpsertSettings(ctx context.Context, s Settings) error

	// events (batched; implementations decide how: unnest+COPY, multi-row VALUES, prepared stmt loop)
	WritePlays(ctx context.Context, plays []Play) error
	WriteMisc(ctx context.Context, misc []Misc) error

	// guilds
	UpsertGuilds(ctx context.Context, guilds []Guild) error
	MarkGuildsLeftExcept(ctx context.Context, present []snowflake.ID) ([]snowflake.ID, error)
	MarkGuildLeft(ctx context.Context, guild snowflake.ID) error
	SeedSettings(ctx context.Context, d Defaults) (int64, error)
	SeedSettingsFor(ctx context.Context, guild snowflake.ID, d Defaults) error

	// opt-outs (bot-wide, per user) and quiet (per guild): append-only, read once at boot and
	// mirrored in memory. List is each one's live row, Set closes it and appends, Clear closes it.
	ListOptOuts(ctx context.Context) ([]Silence, error)
	SetOptOut(ctx context.Context, o Silence) error
	ClearOptOut(ctx context.Context, user snowflake.ID) error
	ListQuiet(ctx context.Context) ([]Silence, error)
	SetQuiet(ctx context.Context, q Silence) error
	ClearQuiet(ctx context.Context, guild snowflake.ID) error

	// chaos (append-only; ListChaos is the newest row of every guild whose window is on)
	ListChaos(ctx context.Context) ([]Chaos, error)
	AppendChaos(ctx context.Context, c Chaos) error

	// stats (a nil guild is every guild)
	GuildStats(ctx context.Context, guild snowflake.ID) (GuildStats, error)
	UserStats(ctx context.Context, guild *snowflake.ID, user snowflake.ID) (UserStats, error)
	HeardSounds(ctx context.Context, guild *snowflake.ID, user snowflake.ID, triggers []string) ([]string, error)
	GlobalStats(ctx context.Context) (GlobalStats, error)
	Leaderboard(ctx context.Context, guild snowflake.ID, board string, days int) ([]Row, error)
	TopGuilds(ctx context.Context, days int) ([]Row, error)

	// ranks (plays at or after since; the totals above stay all-time)
	UserRank(ctx context.Context, guild, user snowflake.ID, since time.Time) (UserRank, error)
	UserRecent(ctx context.Context, user snowflake.ID, since time.Time) (UserCounts, error)
	GuildRecent(ctx context.Context, guild snowflake.ID, since time.Time) (GuildRecent, error)
	// Cuts is metric's 100 percentile cut-points over [since, until), ascending; empty when nobody
	// is in the population. See the Cuts queries for what cut k means.
	Cuts(ctx context.Context, metric string, since, until time.Time) ([]float64, error)

	// series (UTC hours at or after since, ascending, only the hours with something in them; a nil
	// guild is every guild)
	PlaysHourly(ctx context.Context, guild *snowflake.ID, since time.Time) ([]PlayHour, error)
	UserHourly(ctx context.Context, guild *snowflake.ID, user snowflake.ID, since time.Time) ([]UserHour, error)
	// RefreshAnalytics recounts whatever the backend keeps rolled up for the series. A backend that
	// counts on read has nothing to do.
	RefreshAnalytics(ctx context.Context) error
}

// Opener is registered by each backend in its init(); keeps store free of driver imports.
type Opener func(ctx context.Context, url string) (Store, error)

var openers = map[string]Opener{}

func Register(scheme string, o Opener) { openers[scheme] = o }

// Open selects a backend by URL scheme. Both `postgres://` and `postgresql://` map to pg;
// `sqlite://`, `sqlite:`, and `file:` map to sqlite.
func Open(ctx context.Context, url string) (Store, error) {
	scheme, _, ok := strings.Cut(url, ":")
	if !ok {
		return nil, fmt.Errorf("store: DATABASE_URL %q has no scheme", url)
	}
	switch scheme {
	case "postgresql":
		scheme = "postgres"
	case "file", "sqlite3":
		scheme = "sqlite"
	}
	o, ok := openers[scheme]
	if !ok {
		return nil, fmt.Errorf("store: no backend for scheme %q (have: %s)", scheme, keys())
	}
	return o(ctx, url)
}

func keys() string {
	k := make([]string, 0, len(openers))
	for s := range openers {
		k = append(k, s)
	}
	return strings.Join(k, ", ")
}

// WaitAndMigrate is the boot sequence every backend shares: poll Ping with backoff, then Migrate.
func WaitAndMigrate(ctx context.Context, s Store) error {
	deadline := time.Now().Add(60 * time.Second)
	delay := 500 * time.Millisecond
	for {
		err := s.Ping(ctx)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("database not reachable: %w", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
		delay = min(delay*2, 5*time.Second)
	}
	return s.Migrate(ctx)
}
