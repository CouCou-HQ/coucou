// Package events is the append-only stats log: buffered in memory, flushed in batches through the store.
package events

import (
	"context"
	"encoding/json"
	"log/slog"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/disgoorg/snowflake/v2"

	"github.com/be-sandaa/coucou/internal/store"
)

type Trigger string

const (
	TriggerLoop    Trigger = "loop"
	TriggerCommand Trigger = "command"
	TriggerEncore  Trigger = "encore"
)

// collecting is the triggers a sound counts toward a collection from; /play is left out so it
// cannot be farmed.
var collecting = []string{string(TriggerLoop), string(TriggerEncore)}

type Play = store.Play

// The Misc fields that get their own column, named here because the console line uses the same
// keys the table does — a grep that finds the line finds the row.
const (
	keyGuild = "guild"
	keyUser  = "user"
)

// Misc is one recorded event. Guild is nil when the event belongs to no server.
type Misc struct {
	At    time.Time
	Kind  string
	Guild *snowflake.ID
	User  *snowflake.ID
	Data  map[string]any
}

const (
	flushEvery = 5 * time.Second
	flushAt    = 200
	maxBuffer  = 2000
)

type Log struct {
	db    store.Store
	mu    sync.Mutex
	plays []store.Play
	misc  []store.Misc
	kick  chan struct{}
}

func New(db store.Store) *Log { return &Log{db: db, kick: make(chan struct{}, 1)} }

func (l *Log) RecordPlay(p Play) {
	l.mu.Lock()
	if len(l.plays) >= maxBuffer {
		l.plays = l.plays[1:]
	}
	l.plays = append(l.plays, p)
	n := len(l.plays)
	l.mu.Unlock()
	if n >= flushAt {
		select {
		case l.kick <- struct{}{}:
		default:
		}
	}
}

func (l *Log) Record(m Misc) {
	data, err := json.Marshal(m.Data)
	if err != nil {
		slog.Error("events: encoding misc data, storing an empty object", slog.String("kind", m.Kind), slog.Any("err", err))
		data = []byte("{}")
	}
	l.mu.Lock()
	if len(l.misc) >= maxBuffer {
		l.misc = l.misc[1:]
	}
	l.misc = append(l.misc, store.Misc{At: m.At, Kind: m.Kind, Guild: m.Guild, User: m.User, Data: data})
	l.mu.Unlock()
}

// Audit records one permanent event and prints it, in that order and in one call. Both effects
// belong to the same fact, so a caller cannot store a row that the console disagrees with — which
// is what the old slog.Handler bought by intercepting a level, at the price of flattening a struct
// into attrs so it could parse them back into the same struct.
//
// The line goes out at Info. The record's permanence is the row, not the level: a custom level
// between Info and Warn only ever meant "print me", and it cost every handler a rename to stop
// printing "INFO+2".
//
// An unset At means now. Only a caller holding a better time — an event queued minutes ago — sets
// it, and then the row is stored under when the thing happened rather than when this ran.
func (l *Log) Audit(ctx context.Context, m Misc) {
	if m.At.IsZero() {
		m.At = time.Now()
	}
	l.Record(m)
	l.Print(ctx, m)
}

// Print is the console half of Audit on its own, for a change the database records itself. A
// settings change reaches audit.logs through a trigger on guild_settings, with the old and new
// values a Misc cannot carry; recording it here as well would store the same change twice.
func (l *Log) Print(ctx context.Context, m Misc) {
	slog.InfoContext(ctx, m.Kind, auditAttrs(m)...)
}

// auditAttrs renders a record for the console. The data keys are sorted because map order is
// random, and a line whose attrs reshuffle between two identical events is one nobody can grep.
func auditAttrs(m Misc) []any {
	attrs := make([]any, 0, len(m.Data)+2)
	if m.Guild != nil {
		attrs = append(attrs, slog.Any(keyGuild, *m.Guild))
	}
	if m.User != nil {
		attrs = append(attrs, slog.Any(keyUser, *m.User))
	}
	for _, k := range slices.Sorted(maps.Keys(m.Data)) {
		attrs = append(attrs, slog.Any(k, m.Data[k]))
	}
	return attrs
}

// Buffered is the flush backlog, for the gauge that watches it. Past maxBuffer the log drops the
// oldest rows, so this is the only signal that stats are being lost.
func (l *Log) Buffered() (plays, misc int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.plays), len(l.misc)
}

func (l *Log) Run(ctx context.Context) {
	t := time.NewTicker(flushEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			l.Flush(context.Background())
			return
		case <-t.C:
		case <-l.kick:
		}
		l.Flush(ctx)
	}
}

func (l *Log) Flush(ctx context.Context) {
	l.mu.Lock()
	plays, misc := l.plays, l.misc
	l.plays, l.misc = nil, nil
	l.mu.Unlock()
	if len(plays) == 0 && len(misc) == 0 {
		return
	}
	if err := l.db.WritePlays(ctx, plays); err != nil {
		slog.Error("events: plays flush failed, re-queueing", slog.Int("n", len(plays)), slog.Any("err", err))
		l.mu.Lock()
		l.plays = newest(append(plays, l.plays...))
		l.mu.Unlock()
	}
	if err := l.db.WriteMisc(ctx, misc); err != nil {
		slog.Error("events: misc flush failed, re-queueing", slog.Int("n", len(misc)), slog.Any("err", err))
		l.mu.Lock()
		l.misc = newest(append(misc, l.misc...))
		l.mu.Unlock()
	}
}

// newest holds a re-queued batch to maxBuffer. Without it every failed flush adds whatever arrived
// while it was in flight, and an outage grows the buffer for as long as it lasts.
func newest[T any](rows []T) []T {
	if len(rows) > maxBuffer {
		return rows[len(rows)-maxBuffer:]
	}
	return rows
}

// Stats/leaderboards are just the store's; re-exported so commands don't import store directly.
func (l *Log) GuildStats(ctx context.Context, g snowflake.ID) (store.GuildStats, error) {
	return l.db.GuildStats(ctx, g)
}
func (l *Log) UserStats(ctx context.Context, g *snowflake.ID, u snowflake.ID) (store.UserStats, error) {
	return l.db.UserStats(ctx, g, u)
}

// HeardSounds is the distinct sounds u has been caught by in g, or anywhere when g is nil, by the
// triggers that collect.
func (l *Log) HeardSounds(ctx context.Context, g *snowflake.ID, u snowflake.ID) ([]string, error) {
	return l.db.HeardSounds(ctx, g, u, collecting)
}

func (l *Log) UserRank(ctx context.Context, g, u snowflake.ID, since time.Time) (store.UserRank, error) {
	return l.db.UserRank(ctx, g, u, since)
}
func (l *Log) UserRecent(ctx context.Context, u snowflake.ID, since time.Time) (store.UserCounts, error) {
	return l.db.UserRecent(ctx, u, since)
}
func (l *Log) GuildRecent(ctx context.Context, g snowflake.ID, since time.Time) (store.GuildRecent, error) {
	return l.db.GuildRecent(ctx, g, since)
}

func (l *Log) GlobalStats(ctx context.Context) (store.GlobalStats, error) {
	return l.db.GlobalStats(ctx)
}
func (l *Log) Leaderboard(ctx context.Context, g snowflake.ID, board string, days int) ([]store.Row, error) {
	return l.db.Leaderboard(ctx, g, board, days)
}
func (l *Log) PlaysHourly(ctx context.Context, g *snowflake.ID, since time.Time) ([]store.PlayHour, error) {
	return l.db.PlaysHourly(ctx, g, since)
}
func (l *Log) UserHourly(ctx context.Context, g *snowflake.ID, u snowflake.ID, since time.Time) ([]store.UserHour, error) {
	return l.db.UserHourly(ctx, g, u, since)
}
func (l *Log) TopGuilds(ctx context.Context, days int) ([]store.Row, error) {
	return l.db.TopGuilds(ctx, days)
}

func (l *Log) CharacterPlays(ctx context.Context, guild *snowflake.ID) ([]store.Row, error) {
	return l.db.CharacterPlays(ctx, guild)
}
