// Package sqlite is the embedded backend: modernc.org/sqlite (pure Go, no cgo), goose, sqlc.
// Right for a single-process bot on one box; wrong the moment a second process needs the same data.
package sqlite

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/disgoorg/snowflake/v2"
	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite" // registers the pure-Go "sqlite" database/sql driver

	"github.com/be-sandaa/coucou/internal/store"
	"github.com/be-sandaa/coucou/internal/store/sqlite/gen"
)

//#region Lifecycle

//go:embed migrations/*.sql
var migrations embed.FS

// gooseTable is the default name. SQLite has one namespace — no schema to move the version table
// into, the way the postgres backend does — but it is named here so that neither backend depends on
// which one ran last.
const gooseTable = "goose_db_version"

func init() { store.Register("sqlite", Open) }

type Store struct {
	db *sql.DB
	q  *gen.Queries
}

// Open accepts sqlite://path, sqlite:path, file:path. Enables WAL + foreign keys; single writer conn.
func Open(_ context.Context, url string) (store.Store, error) {
	path := strings.TrimPrefix(strings.TrimPrefix(strings.TrimPrefix(url, "sqlite://"), "sqlite:"), "file:")
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // sqlite is single-writer; one conn avoids SQLITE_BUSY dances entirely
	return &Store{db: db, q: gen.New(db)}, nil
}

func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }
func (s *Store) Close() {
	if err := s.db.Close(); err != nil {
		slog.Warn("db: closing sqlite", slog.Any("err", err))
	}
}

func (s *Store) Migrate(ctx context.Context) error {
	goose.SetBaseFS(migrations)
	goose.SetLogger(goose.NopLogger())
	// Stated rather than left at the default, because goose keeps the table name in a package
	// global: the postgres backend points it at its own schema, and a process that opened both —
	// the store suite does — would otherwise have SQLite look for a schema it has no concept of.
	goose.SetTableName(gooseTable)
	if err := goose.SetDialect("sqlite3"); err != nil {
		return err
	}
	// A fresh database has no goose version table yet, so this is informational only.
	before, err := goose.GetDBVersionContext(ctx, s.db)
	if err != nil {
		slog.Debug("db: no goose version yet", slog.Any("err", err))
	}
	if err := goose.UpContext(ctx, s.db, "migrations"); err != nil {
		return err
	}
	after, err := goose.GetDBVersionContext(ctx, s.db)
	if err != nil {
		return fmt.Errorf("goose: reading version after migrating: %w", err)
	}
	slog.Info("db: sqlite", slog.Int64("version", after), slog.Int64("applied", after-before))
	return nil
}

const ts = time.RFC3339Nano

func fmtT(t time.Time) string { return t.UTC().Format(ts) }

// fmtTp is fmtT for an optional timestamp: nil stays nil, which is the nullable column.
func fmtTp(t *time.Time) *string {
	if t == nil {
		return nil
	}
	v := fmtT(*t)
	return &v
}

// parseT is the inverse, for the one timestamp that is read back out rather than compared in SQL.
// A value that will not parse reads as absent, which is what the missing row already means here.
func parseT(v *string) *time.Time {
	if v == nil {
		return nil
	}
	t, err := time.Parse(ts, *v)
	if err != nil {
		return nil
	}
	return &t
}

func since(days int) string {
	if days <= 0 {
		return ""
	}
	return fmtT(time.Now().Add(-time.Duration(days) * 24 * time.Hour))
}

//#endregion

//#region Settings

func (s *Store) ListSettings(ctx context.Context) ([]store.Settings, error) {
	rows, err := s.q.ListSettings(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]store.Settings, len(rows))
	for i, r := range rows {
		out[i] = store.Settings{Guild: sid(r.GuildID), Chance: int(r.JoinChance), QuietFrom: i64p(r.QuietFrom), QuietTo: i64p(r.QuietTo), TZ: r.Tz, Suspense: int(r.Suspense), FakeOut: int(r.Fakeout), Encore: int(r.Encore)}
	}
	return out, nil
}

// UpsertSettings writes the row and its audit record together, so neither can exist without the
// other: the postgres backend gets that from a trigger, this one from the transaction.
func (s *Store) UpsertSettings(ctx context.Context, st store.Settings) error {
	id := i64(st.Guild)
	return s.audited(ctx, change{schema: schemaGuilds, table: tableSettings, pkColumn: colGuildID, pk: id},
		func(q *gen.Queries) (map[string]any, error) { return getRow(q.GetSettings(ctx, id)) },
		func(q *gen.Queries) error {
			return q.UpsertSettings(ctx, gen.UpsertSettingsParams{
				GuildID: id, JoinChance: int64(st.Chance), QuietFrom: pi64(st.QuietFrom), QuietTo: pi64(st.QuietTo), Tz: st.TZ, Suspense: int64(st.Suspense), Fakeout: int64(st.FakeOut), Encore: int64(st.Encore),
				UpdatedBy: nullID(st.UpdatedBy),
			})
		})
}

//#endregion

//#region Events

func (s *Store) WritePlays(ctx context.Context, plays []store.Play) error {
	if len(plays) == 0 {
		return nil
	}
	return s.tx(ctx, func(q *gen.Queries) error {
		for _, p := range plays {
			if err := insertPlay(ctx, q, p); err != nil {
				return err
			}
		}
		return nil
	})
}

// insertPlay writes one play and its listener pairs. A failed play logs only the ones who fled:
// nobody heard anything, but leaving is still leaving.
func insertPlay(ctx context.Context, q *gen.Queries, p store.Play) error {
	var reason *string
	if p.Reason != "" {
		reason = &p.Reason
	}
	id, err := q.InsertPlay(ctx, gen.InsertPlayParams{
		At: fmtT(p.At), GuildID: i64(p.Guild), ChannelID: i64(p.Channel), Sound: p.Sound, Trigger: p.Trigger,
		UserID: idp(p.User), Listeners: int64(len(p.ListenerIDs)), Ok: b2i(p.OK), Reason: reason,
		DurationMs: p.Duration.Milliseconds(),
	})
	if err != nil {
		return err
	}
	for _, u := range p.ListenerIDs {
		fled := slices.Contains(p.FledIDs, u)
		if !p.OK && !fled {
			continue
		}
		if err := q.InsertPlayListener(ctx, gen.InsertPlayListenerParams{PlayID: id, UserID: i64(u), Fled: b2i(fled)}); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) WriteMisc(ctx context.Context, misc []store.Misc) error {
	if len(misc) == 0 {
		return nil
	}
	return s.tx(ctx, func(q *gen.Queries) error {
		for _, m := range misc {
			data := "{}"
			if m.Data != nil {
				data = string(m.Data)
			}
			if err := q.InsertEvent(ctx, gen.InsertEventParams{At: fmtT(m.At), Kind: m.Kind, GuildID: idp(m.Guild), UserID: idp(m.User), Data: data}); err != nil {
				return err
			}
		}
		return nil
	})
}

//#endregion

//#region Guilds

func (s *Store) UpsertGuilds(ctx context.Context, gs []store.Guild) error {
	return s.tx(ctx, func(q *gen.Queries) error {
		for _, g := range gs {
			if err := q.UpsertGuild(ctx, gen.UpsertGuildParams{GuildID: i64(g.ID), JoinedAt: fmtT(g.JoinedAt)}); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Store) MarkGuildsLeftExcept(ctx context.Context, present []snowflake.ID) ([]snowflake.ID, error) {
	keep := make(map[int64]bool, len(present))
	for _, p := range present {
		keep[i64(p)] = true
	}
	var left []snowflake.ID
	err := s.tx(ctx, func(q *gen.Queries) error {
		ids, err := q.PresentGuilds(ctx)
		if err != nil {
			return err
		}
		for _, id := range ids {
			if keep[id] {
				continue
			}
			if err := q.MarkGuildLeft(ctx, id); err != nil {
				return err
			}
			left = append(left, sid(id))
		}
		return nil
	})
	return left, err
}

func (s *Store) MarkGuildLeft(ctx context.Context, g snowflake.ID) error {
	return s.q.MarkGuildLeft(ctx, i64(g))
}
func (s *Store) SeedSettings(ctx context.Context, dc int) (int64, error) {
	return s.q.SeedSettingsForGuilds(ctx, int64(dc))
}
func (s *Store) SeedSettingsFor(ctx context.Context, g snowflake.ID, dc int) error {
	return s.q.SeedSettingsForGuild(ctx, gen.SeedSettingsForGuildParams{GuildID: i64(g), JoinChance: int64(dc)})
}

//#endregion

//#region Opt-outs

func (s *Store) ListOptOuts(ctx context.Context) ([]store.OptOut, error) {
	now := fmtT(time.Now())
	rows, err := s.q.ListOptOuts(ctx, &now)
	if err != nil {
		return nil, err
	}
	out := make([]store.OptOut, len(rows))
	for i, r := range rows {
		o := store.OptOut{
			User:  sid(r.UserID),
			Until: parseT(r.Until),
		}
		if r.Rrule != nil {
			o.Rule = *r.Rrule
		}
		if r.WindowS != nil {
			o.Window = time.Duration(*r.WindowS) * time.Second
		}
		out[i] = o
	}
	return out, nil
}

func (s *Store) SetOptOut(ctx context.Context, o store.OptOut) error {
	id := i64(o.User)
	return s.audited(ctx, change{schema: schemaUsers, table: tableOptouts, pkColumn: colUserID, pk: id},
		func(q *gen.Queries) (map[string]any, error) { return getRow(q.GetOptOut(ctx, id)) },
		func(q *gen.Queries) error {
			return q.SetOptOut(ctx, gen.SetOptOutParams{
				UserID: id, Until: fmtTp(o.Until), Rrule: strp(o.Rule), WindowS: secs(o.Window),
			})
		})
}

// strp and secs map the two "not set" shapes onto the null columns: a row with no schedule has no
// rule and no window, and "" or 0 in those columns would read as a schedule that never fires.
func strp(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func secs(d time.Duration) *int64 {
	if d <= 0 {
		return nil
	}
	v := int64(d / time.Second)
	return &v
}

// ClearOptOut records nothing when there was no opt-out to clear: both sides are then empty, and a
// delete that deleted nothing is not a change.
func (s *Store) ClearOptOut(ctx context.Context, user snowflake.ID) error {
	id := i64(user)
	return s.audited(ctx, change{schema: schemaUsers, table: tableOptouts, pkColumn: colUserID, pk: id},
		func(q *gen.Queries) (map[string]any, error) { return getRow(q.GetOptOut(ctx, id)) },
		func(q *gen.Queries) error { return q.ClearOptOut(ctx, id) })
}

//#endregion

//#region Chaos

func (s *Store) ListChaos(ctx context.Context) ([]store.Chaos, error) {
	rows, err := s.q.ListChaos(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]store.Chaos, 0, len(rows))
	for _, r := range rows {
		if r.Rrule == nil || r.Hours == nil || r.Chance == nil {
			continue
		}
		out = append(out, store.Chaos{Guild: sid(r.GuildID), Rule: *r.Rrule, Hours: int(*r.Hours), Chance: int(*r.Chance)})
	}
	return out, nil
}

// AppendChaos is not audited: the table is append-only, so it is its own history.
func (s *Store) AppendChaos(ctx context.Context, c store.Chaos) error {
	p := gen.InsertChaosParams{GuildID: i64(c.Guild), Rrule: strp(c.Rule), CreatedBy: i64(c.CreatedBy)}
	if c.Rule != "" {
		p.Hours, p.Chance = pi64(&c.Hours), pi64(&c.Chance)
	}
	return s.q.InsertChaos(ctx, p)
}

//#endregion

//#region Stats

func (s *Store) GuildStats(ctx context.Context, g snowflake.ID) (store.GuildStats, error) {
	r, err := s.q.GuildStats(ctx, i64(g))
	if err != nil {
		return store.GuildStats{}, err
	}
	return store.GuildStats{PlaysAll: int(r.PlaysAll), Plays7d: int(r.Plays7d), FailRate7d: r.FailRate7d, TopSound: r.TopSound, LoudestHour: i64p(r.LoudestHour), AvgListeners: r.AvgListeners}, nil
}

func (s *Store) UserStats(ctx context.Context, guild *snowflake.ID, user snowflake.ID) (store.UserStats, error) {
	r, err := s.q.UserStats(ctx, gen.UserStatsParams{GuildID: idp(guild), UserID: i64(user)})
	if err != nil {
		return store.UserStats{}, err
	}
	triggered, fled := 0, 0
	if r.Triggered != nil {
		triggered = int(*r.Triggered)
	}
	if r.Fled != nil {
		fled = int(*r.Fled)
	}
	return store.UserStats{Heard: int(r.Heard), Triggered: triggered, Fled: fled, LastHeard: parseT(r.LastHeard)}, nil
}

func (s *Store) HeardSounds(ctx context.Context, guild *snowflake.ID, user snowflake.ID, triggers []string) ([]string, error) {
	return s.q.HeardSounds(ctx, gen.HeardSoundsParams{GuildID: idp(guild), UserID: i64(user), Triggers: triggers})
}

func (s *Store) GlobalStats(ctx context.Context) (store.GlobalStats, error) {
	r, err := s.q.GlobalStats(ctx)
	if err != nil {
		return store.GlobalStats{}, err
	}
	return store.GlobalStats{Plays24h: int(r.Plays24h), Guilds24h: int(r.Guilds24h), TopSound: r.TopSound}, nil
}

func (s *Store) Leaderboard(ctx context.Context, g snowflake.ID, board string, days int) ([]store.Row, error) {
	id, sc := i64(g), since(days)
	switch board {
	case "heard":
		return rows(s.q.BoardHeard(ctx, gen.BoardHeardParams{GuildID: id, Column2: sc}))
	case "triggered":
		return rows(s.q.BoardTriggered(ctx, gen.BoardTriggeredParams{GuildID: id, Column2: sc}))
	case "fled":
		return rows(s.q.BoardFled(ctx, gen.BoardFledParams{GuildID: id, Column2: sc}))
	case "sounds":
		return rows(s.q.BoardSounds(ctx, gen.BoardSoundsParams{GuildID: id, Column2: sc}))
	case "channels":
		return rows(s.q.BoardChannels(ctx, gen.BoardChannelsParams{GuildID: id, Column2: sc}))
	}
	return nil, fmt.Errorf("unknown board %q", board)
}

func (s *Store) TopGuilds(ctx context.Context, days int) ([]store.Row, error) {
	return rows(s.q.TopGuilds(ctx, since(days)))
}

func (s *Store) UserRank(ctx context.Context, guild, user snowflake.ID, since time.Time) (store.UserRank, error) {
	r, err := s.q.UserRank(ctx, gen.UserRankParams{GuildID: i64(guild), UserID: i64(user), Since: fmtT(since)})
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return store.UserRank{}, nil
	case err != nil:
		return store.UserRank{}, err
	}
	return store.UserRank{
		UserCounts: store.UserCounts{Heard: int(r.Heard), Triggered: int(r.Triggered), Fled: int(r.Fled)},
		HeardBelow: int(r.HeardBelow), TriggeredBelow: int(r.TriggeredBelow), FledBelow: int(r.FledBelow), Of: int(r.Total),
	}, nil
}

func (s *Store) UserRecent(ctx context.Context, user snowflake.ID, since time.Time) (store.UserCounts, error) {
	r, err := s.q.UserRecent(ctx, gen.UserRecentParams{UserID: i64(user), Since: fmtT(since)})
	if err != nil {
		return store.UserCounts{}, err
	}
	return store.UserCounts{Heard: int(r.Heard), Triggered: int(r.Triggered), Fled: int(r.Fled)}, nil
}

func (s *Store) GuildRecent(ctx context.Context, guild snowflake.ID, since time.Time) (store.GuildRecent, error) {
	r, err := s.q.GuildRecent(ctx, gen.GuildRecentParams{GuildID: i64(guild), Since: fmtT(since)})
	if err != nil {
		return store.GuildRecent{}, err
	}
	return store.GuildRecent{Plays: int(r.Plays), AvgListeners: r.AvgListeners}, nil
}

func (s *Store) Cuts(ctx context.Context, metric string, since, until time.Time) ([]float64, error) {
	from, to := fmtT(since), fmtT(until)
	switch metric {
	case store.MetricHeard:
		return s.q.CutsHeard(ctx, gen.CutsHeardParams{Since: from, Until: to})
	case store.MetricTriggered:
		return s.q.CutsTriggered(ctx, gen.CutsTriggeredParams{Since: from, Until: to})
	case store.MetricFled:
		return s.q.CutsFled(ctx, gen.CutsFledParams{Since: from, Until: to})
	case store.MetricPlays:
		return s.q.CutsPlays(ctx, gen.CutsPlaysParams{Since: from, Until: to})
	case store.MetricListeners:
		return s.q.CutsListeners(ctx, gen.CutsListenersParams{Since: from, Until: to})
	}
	return nil, fmt.Errorf("unknown metric %q", metric)
}

// hourLayout is the 13-character prefix of fmtT that names an hour; see the PlaysHourly query.
const hourLayout = "2006-01-02T15"

func hourKey(t time.Time) string { return t.UTC().Format(hourLayout) }

// parseHour reads an hour key back. The key was cut from a timestamp this package wrote, so a
// failure is a corrupt row and fails the whole series rather than drawing a hole in it.
func parseHour(v string) (time.Time, error) { return time.Parse(hourLayout, v) }

func (s *Store) PlaysHourly(ctx context.Context, guild *snowflake.ID, since time.Time) ([]store.PlayHour, error) {
	rs, err := s.q.PlaysHourly(ctx, gen.PlaysHourlyParams{GuildID: idp(guild), Since: hourKey(since)})
	if err != nil {
		return nil, err
	}
	out := make([]store.PlayHour, len(rs))
	for i, r := range rs {
		h, err := parseHour(r.Hour)
		if err != nil {
			return nil, err
		}
		out[i] = store.PlayHour{
			Hour: h, Plays: int(r.Plays), Failed: int(r.Failed), FakeOuts: int(r.Fakeouts),
			Loops: int(r.Loops), Commands: int(r.Commands), Encores: int(r.Encores),
			Listeners: int(r.Listeners), Guilds: int(r.Guilds),
		}
	}
	return out, nil
}

func (s *Store) UserHourly(ctx context.Context, guild *snowflake.ID, user snowflake.ID, since time.Time) ([]store.UserHour, error) {
	rs, err := s.q.UserHourly(ctx, gen.UserHourlyParams{GuildID: idp(guild), UserID: i64(user), Since: hourKey(since)})
	if err != nil {
		return nil, err
	}
	out := make([]store.UserHour, len(rs))
	for i, r := range rs {
		h, err := parseHour(r.Hour)
		if err != nil {
			return nil, err
		}
		out[i] = store.UserHour{Hour: h, Heard: int(r.Heard), Fled: int(r.Fled), Triggered: int(r.Triggered)}
	}
	return out, nil
}

// RefreshAnalytics has nothing to recount: this backend counts the series on read.
func (s *Store) RefreshAnalytics(context.Context) error { return nil }

//#endregion

//#region Helpers

func (s *Store) tx(ctx context.Context, fn func(*gen.Queries) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(s.q.WithTx(tx)); err != nil {
		if rerr := tx.Rollback(); rerr != nil && !errors.Is(rerr, sql.ErrTxDone) {
			return errors.Join(err, rerr)
		}
		return err
	}
	return tx.Commit()
}

func rows[T ~struct {
	Key string `json:"key"`
	N   int64  `json:"n"`
}](in []T, err error) ([]store.Row, error) {
	if err != nil {
		return nil, err
	}
	out := make([]store.Row, len(in))
	for i, r := range in {
		v := struct {
			Key string `json:"key"`
			N   int64  `json:"n"`
		}(r)
		out[i] = store.Row{Key: v.Key, N: int(v.N)}
	}
	return out, nil
}

// SQLite has one integer type, so every id and bounded setting is an int64 column. These are the
// only places the conversions happen.

// i64 converts a snowflake to the integer column type; a snowflake is 63-bit, so this is exact.
func i64(id snowflake.ID) int64 { return int64(id) } //nolint:gosec // snowflakes are 63-bit

// sid is the inverse of i64, for ids read back out of the database.
func sid(v int64) snowflake.ID { return snowflake.ID(v) } //nolint:gosec // round trip of i64

// nullID is i64 for a snowflake that may be absent: zero means the bot acted on its own, and the
// column holds null rather than a user id nobody has.
func nullID(id snowflake.ID) *int64 {
	if id == 0 {
		return nil
	}
	v := i64(id)
	return &v
}

func b2i(b bool) int64 {
	if b {
		return 1
	}
	return 0
}
func idp(v *snowflake.ID) *int64 {
	if v == nil {
		return nil
	}
	i := i64(*v)
	return &i
}
func i64p(v *int64) *int {
	if v == nil {
		return nil
	}
	i := int(*v)
	return &i
}
func pi64(v *int) *int64 {
	if v == nil {
		return nil
	}
	i := int64(*v)
	return &i
}

//#endregion
