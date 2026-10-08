// Package pg is the PostgreSQL backend: pgx/v5 pool, goose migrations, sqlc-generated queries.
package pg

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"slices"
	"time"

	"github.com/disgoorg/snowflake/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/be-sandaa/coucou/internal/store"
	"github.com/be-sandaa/coucou/internal/store/pg/gen"
)

//#region Lifecycle

//go:embed migrations/*.sql
var migrations embed.FS

const migrationLock int64 = 0x50514d47

// The version table lives in its own schema, for the same reason every other table does. goose's
// postgres dialect understands a schema-qualified name — it splits on the dot to look the table up
// in pg_tables — but it will not create the schema, and it reads the table before it would run any
// migration. So both the schema and the move of an existing table happen in Go, ahead of goose.
const gooseTable = "goose.migrations"

// prepareGooseSchema puts the version table where gooseTable says it is, before goose looks for it.
// Idempotent and ordered, run under the migration lock on the connection that holds it.
//
// The two alters are the upgrade path: a database migrated under the old name keeps its history.
// Without them goose would find no table, conclude nothing had been applied, and replay 00001 onto
// tables that already exist.
func prepareGooseSchema(ctx context.Context, conn *sql.Conn) error {
	for _, q := range []string{
		`create schema if not exists goose`,
		`alter table if exists public.goose_db_version set schema goose`,
		`alter table if exists goose.goose_db_version rename to migrations`,
	} {
		if _, err := conn.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("prepare goose schema: %w", err)
		}
	}
	return nil
}

func init() { store.Register("postgres", Open) }

type Store struct {
	pool *pgxpool.Pool
	q    *gen.Queries
}

func Open(ctx context.Context, url string) (store.Store, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, err
	}
	cfg.MaxConns, cfg.MinConns, cfg.MaxConnIdleTime = 2, 1, 5*time.Minute
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return &Store{pool: pool, q: gen.New(pool)}, nil
}

func (s *Store) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }
func (s *Store) Close()                         { s.pool.Close() }

// lockMigrations takes the advisory lock on one dedicated connection, so that two replicas booting
// together cannot run the migrations at the same time. The returned release drops the lock and the
// connection with it, and is safe to defer the moment this returns without error.
func lockMigrations(ctx context.Context, db *sql.DB) (*sql.Conn, func(), error) {
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, nil, err
	}
	release := func() {
		// Released on a background context: ctx may already be cancelled, and leaving the lock
		// held would block every future boot.
		if _, err := conn.ExecContext(context.Background(), `select pg_advisory_unlock($1)`, migrationLock); err != nil {
			slog.Warn("db: releasing migration lock", slog.Any("err", err))
		}
		if err := conn.Close(); err != nil {
			slog.Warn("db: closing migration lock conn", slog.Any("err", err))
		}
	}
	if _, err := conn.ExecContext(ctx, `select pg_advisory_lock($1)`, migrationLock); err != nil {
		release()
		return nil, nil, err
	}
	return conn, release, nil
}

func (s *Store) Migrate(ctx context.Context) error {
	goose.SetBaseFS(migrations)
	goose.SetLogger(goose.NopLogger())
	goose.SetTableName(gooseTable)
	if err := goose.SetDialect("postgres"); err != nil {
		return err
	}
	sqlDB := stdlib.OpenDBFromPool(s.pool)
	defer func() {
		if err := sqlDB.Close(); err != nil {
			slog.Warn("db: closing migration handle", slog.Any("err", err))
		}
	}()

	lock, release, err := lockMigrations(ctx, sqlDB)
	if err != nil {
		return err
	}
	defer release()

	if err := prepareGooseSchema(ctx, lock); err != nil {
		return err
	}

	// A fresh database has no goose version table yet, so this is informational only.
	before, err := goose.GetDBVersionContext(ctx, sqlDB)
	if err != nil {
		slog.Debug("db: no goose version yet", slog.Any("err", err))
	}
	if err := goose.UpContext(ctx, sqlDB, "migrations"); err != nil {
		return err
	}
	after, err := goose.GetDBVersionContext(ctx, sqlDB)
	if err != nil {
		return fmt.Errorf("goose: reading version after migrating: %w", err)
	}
	slog.Info("db: postgres", slog.Int64("version", after), slog.Int64("applied", after-before))
	return nil
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
		out[i] = store.Settings{Guild: sid(r.GuildID), Chance: int(r.JoinChance), TZ: r.Tz, Suspense: int(r.Suspense), FakeOut: int(r.Fakeout), Encore: int(r.Encore), NSFW: r.Nsfw, Character: deref(r.Character)}
	}
	return out, nil
}

func (s *Store) UpsertSettings(ctx context.Context, st store.Settings) error {
	return s.q.UpsertSettings(ctx, gen.UpsertSettingsParams{
		GuildID: i64(st.Guild), JoinChance: small(st.Chance), Tz: st.TZ, Suspense: small(st.Suspense), Fakeout: small(st.FakeOut), Encore: small(st.Encore), Nsfw: st.NSFW, Character: strp(st.Character),
		UpdatedBy: i64(st.UpdatedBy),
	})
}

//#endregion

//#region Events

func (s *Store) WritePlays(ctx context.Context, plays []store.Play) error {
	if len(plays) == 0 {
		return nil
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		n := len(plays)
		p := gen.InsertPlaysParams{
			At: make([]time.Time, n), GuildIds: make([]int64, n), ChannelIds: make([]int64, n), Sounds: make([]string, n),
			Triggers: make([]string, n), UserIds: make([]int64, n), Listeners: make([]int16, n), Oks: make([]bool, n),
			Reasons: make([]string, n), DurationsMs: make([]int32, n), Characters: make([]string, n),
		}
		for i, pl := range plays {
			p.At[i], p.GuildIds[i], p.ChannelIds[i], p.Sounds[i], p.Triggers[i] = pl.At, i64(pl.Guild), i64(pl.Channel), pl.Sound, pl.Trigger
			// 0 / "" are the sentinels the query turns back into NULL — sqlc cannot type an array
			// with nullable elements, and neither a snowflake nor a failure reason is ever zero.
			if pl.User != nil {
				p.UserIds[i] = i64(*pl.User)
			}
			p.Reasons[i], p.Characters[i] = pl.Reason, pl.Character
			p.Listeners[i], p.Oks[i], p.DurationsMs[i] = small(len(pl.ListenerIDs)), pl.OK, i32(int(pl.Duration.Milliseconds()))
		}
		ids, err := q.InsertPlays(ctx, p)
		if err != nil {
			return err
		}
		var pairs []gen.InsertPlayListenersParams
		for i, pl := range plays {
			for _, u := range pl.ListenerIDs {
				fled := slices.Contains(pl.FledIDs, u)
				if !pl.OK && !fled {
					continue
				}
				pairs = append(pairs, gen.InsertPlayListenersParams{PlayID: ids[i], UserID: i64(u), Fled: fled})
			}
		}
		if len(pairs) > 0 {
			_, err = q.InsertPlayListeners(ctx, pairs) // COPY
		}
		return err
	})
}

func (s *Store) WriteMisc(ctx context.Context, misc []store.Misc) error {
	if len(misc) == 0 {
		return nil
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		for _, m := range misc {
			data := m.Data
			if data == nil {
				data = []byte("{}")
			}
			if err := q.InsertEvent(ctx, gen.InsertEventParams{At: m.At, Kind: m.Kind, GuildID: idp(m.Guild), UserID: idp(m.User), Data: data}); err != nil {
				return err
			}
		}
		return nil
	})
}

//#endregion

//#region Guilds

func (s *Store) UpsertGuilds(ctx context.Context, gs []store.Guild) error {
	if len(gs) == 0 {
		return nil
	}
	p := gen.UpsertGuildsParams{}
	for _, g := range gs {
		p.GuildIds = append(p.GuildIds, i64(g.ID))
		p.JoinedAts = append(p.JoinedAts, g.JoinedAt)
	}
	return s.q.UpsertGuilds(ctx, p)
}

func (s *Store) MarkGuildsLeftExcept(ctx context.Context, present []snowflake.ID) ([]snowflake.ID, error) {
	ids := make([]int64, len(present))
	for i, p := range present {
		ids[i] = i64(p)
	}
	left, err := s.q.MarkGuildsLeftExcept(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make([]snowflake.ID, len(left))
	for i, l := range left {
		out[i] = sid(l)
	}
	return out, nil
}

func (s *Store) MarkGuildLeft(ctx context.Context, g snowflake.ID) error {
	return s.q.MarkGuildLeft(ctx, i64(g))
}
func (s *Store) SeedSettings(ctx context.Context, d store.Defaults) (int64, error) {
	return s.q.SeedSettingsForGuilds(ctx, gen.SeedSettingsForGuildsParams{
		JoinChance: small(d.Chance), Suspense: small(d.Suspense), Fakeout: small(d.FakeOut), Encore: small(d.Encore),
	})
}
func (s *Store) SeedSettingsFor(ctx context.Context, g snowflake.ID, d store.Defaults) error {
	return s.q.SeedSettingsForGuild(ctx, gen.SeedSettingsForGuildParams{
		GuildID: i64(g), JoinChance: small(d.Chance), Suspense: small(d.Suspense), Fakeout: small(d.FakeOut), Encore: small(d.Encore),
	})
}

//#endregion

//#region Silences

func (s *Store) ListOptOuts(ctx context.Context) ([]store.Silence, error) {
	rows, err := s.q.ListOptOuts(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]store.Silence, len(rows))
	for i, r := range rows {
		out[i] = silence(r.UserID, r.Rrule, r.WindowS, r.DisabledAt)
	}
	return out, nil
}

// SetOptOut closes the live row and appends the new one in one transaction, so a failed append
// leaves the old one standing rather than nothing.
func (s *Store) SetOptOut(ctx context.Context, o store.Silence) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		if err := q.CloseOptOut(ctx, i64(o.ID)); err != nil {
			return err
		}
		return q.InsertOptOut(ctx, gen.InsertOptOutParams{
			UserID: i64(o.ID), Rrule: strp(o.Rule), WindowS: secs(o.Window), DisabledAt: o.Until,
		})
	})
}

func (s *Store) ClearOptOut(ctx context.Context, user snowflake.ID) error {
	return s.q.CloseOptOut(ctx, i64(user))
}

func (s *Store) ListQuiet(ctx context.Context) ([]store.Silence, error) {
	rows, err := s.q.ListQuiet(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]store.Silence, len(rows))
	for i, r := range rows {
		out[i] = silence(r.GuildID, r.Rrule, r.WindowS, r.DisabledAt)
	}
	return out, nil
}

func (s *Store) SetQuiet(ctx context.Context, q store.Silence) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		qt := s.q.WithTx(tx)
		if err := qt.CloseQuiet(ctx, i64(q.ID)); err != nil {
			return err
		}
		return qt.InsertQuiet(ctx, gen.InsertQuietParams{
			GuildID: i64(q.ID), Rrule: strp(q.Rule), WindowS: secs(q.Window), DisabledAt: q.Until, CreatedBy: i64(q.By),
		})
	})
}

func (s *Store) ClearQuiet(ctx context.Context, guild snowflake.ID) error {
	return s.q.CloseQuiet(ctx, i64(guild))
}

// strp and secs map the "all the time" shape onto the null columns: "" or 0 there would read as a
// schedule that never fires.
func strp(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func secs(d time.Duration) *int32 {
	if d <= 0 {
		return nil
	}
	v := i32(int(d / time.Second))
	return &v
}

// silence assembles a row's optional columns into the one shape the interface promises.
func silence(id int64, rule *string, window *int32, until *time.Time) store.Silence {
	o := store.Silence{ID: snowflake.ID(id), Until: until} //nolint:gosec // G115: a Discord id round-trips through bigint
	if rule != nil {
		o.Rule = *rule
	}
	if window != nil {
		o.Window = time.Duration(*window) * time.Second
	}
	return o
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

// AppendChaos writes an off as nulls across the board, which is what the table's check expects.
func (s *Store) AppendChaos(ctx context.Context, c store.Chaos) error {
	p := gen.InsertChaosParams{GuildID: i64(c.Guild), Rrule: strp(c.Rule), CreatedBy: i64(c.CreatedBy)}
	if c.Rule != "" {
		p.Hours, p.Chance = pi16(&c.Hours), pi16(&c.Chance)
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
	return store.GuildStats{PlaysAll: int(r.PlaysAll), Plays7d: int(r.Plays7d), FailRate7d: r.FailRate7d, TopSound: r.TopSound, LoudestHour: i32p(r.LoudestHour), AvgListeners: r.AvgListeners}, nil
}

func (s *Store) UserStats(ctx context.Context, guild *snowflake.ID, user snowflake.ID) (store.UserStats, error) {
	r, err := s.q.UserStats(ctx, gen.UserStatsParams{GuildID: idp(guild), UserID: i64(user)})
	if err != nil {
		return store.UserStats{}, err
	}
	// Triggered is left-joined off a CTE that always has a row, so the pointer sqlc gives it is
	// never nil in practice; treating it as nil-able costs less than a query written to prove that.
	triggered, fled := 0, 0
	if r.Triggered != nil {
		triggered = int(*r.Triggered)
	}
	if r.Fled != nil {
		fled = int(*r.Fled)
	}
	return store.UserStats{Heard: int(r.Heard), Triggered: triggered, Fled: fled, LastHeard: r.LastHeard}, nil
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
	id, d := i64(g), i32(days)
	switch board {
	case "heard":
		return rows(s.q.BoardHeard(ctx, gen.BoardHeardParams{GuildID: id, Days: d}))
	case "triggered":
		return rows(s.q.BoardTriggered(ctx, gen.BoardTriggeredParams{GuildID: id, Days: d}))
	case "fled":
		return rows(s.q.BoardFled(ctx, gen.BoardFledParams{GuildID: id, Days: d}))
	case "sounds":
		return rows(s.q.BoardSounds(ctx, gen.BoardSoundsParams{GuildID: id, Days: d}))
	case "channels":
		return rows(s.q.BoardChannels(ctx, gen.BoardChannelsParams{GuildID: id, Days: d}))
	}
	return nil, fmt.Errorf("unknown board %q", board)
}

func (s *Store) TopGuilds(ctx context.Context, days int) ([]store.Row, error) {
	return rows(s.q.TopGuilds(ctx, i32(days)))
}

func (s *Store) UserRank(ctx context.Context, guild, user snowflake.ID, since time.Time) (store.UserRank, error) {
	r, err := s.q.UserRank(ctx, gen.UserRankParams{GuildID: i64(guild), UserID: i64(user), Since: since})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
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
	r, err := s.q.UserRecent(ctx, gen.UserRecentParams{UserID: i64(user), Since: since})
	if err != nil {
		return store.UserCounts{}, err
	}
	return store.UserCounts{Heard: int(r.Heard), Triggered: int(r.Triggered), Fled: int(r.Fled)}, nil
}

func (s *Store) GuildRecent(ctx context.Context, guild snowflake.ID, since time.Time) (store.GuildRecent, error) {
	r, err := s.q.GuildRecent(ctx, gen.GuildRecentParams{GuildID: i64(guild), Since: since})
	if err != nil {
		return store.GuildRecent{}, err
	}
	return store.GuildRecent{Plays: int(r.Plays), AvgListeners: r.AvgListeners}, nil
}

func (s *Store) Cuts(ctx context.Context, metric string, since, until time.Time) ([]float64, error) {
	switch metric {
	case store.MetricHeard:
		return s.q.CutsHeard(ctx, gen.CutsHeardParams{Since: since, Until: until})
	case store.MetricTriggered:
		return s.q.CutsTriggered(ctx, gen.CutsTriggeredParams{Since: since, Until: until})
	case store.MetricFled:
		return s.q.CutsFled(ctx, gen.CutsFledParams{Since: since, Until: until})
	case store.MetricPlays:
		return s.q.CutsPlays(ctx, gen.CutsPlaysParams{Since: since, Until: until})
	case store.MetricListeners:
		return s.q.CutsListeners(ctx, gen.CutsListenersParams{Since: since, Until: until})
	}
	return nil, fmt.Errorf("unknown metric %q", metric)
}

func (s *Store) PlaysHourly(ctx context.Context, guild *snowflake.ID, since time.Time) ([]store.PlayHour, error) {
	rs, err := s.q.PlaysHourly(ctx, gen.PlaysHourlyParams{GuildID: idp(guild), Since: since})
	if err != nil {
		return nil, err
	}
	out := make([]store.PlayHour, len(rs))
	for i, r := range rs {
		out[i] = store.PlayHour{
			Hour: r.Hour, Plays: int(r.Plays), Failed: int(r.Failed), FakeOuts: int(r.Fakeouts),
			Loops: int(r.Loops), Commands: int(r.Commands), Encores: int(r.Encores),
			Listeners: int(r.Listeners), Guilds: int(r.Guilds),
		}
	}
	return out, nil
}

func (s *Store) UserHourly(ctx context.Context, guild *snowflake.ID, user snowflake.ID, since time.Time) ([]store.UserHour, error) {
	rs, err := s.q.UserHourly(ctx, gen.UserHourlyParams{GuildID: idp(guild), UserID: i64(user), Since: since})
	if err != nil {
		return nil, err
	}
	out := make([]store.UserHour, len(rs))
	for i, r := range rs {
		out[i] = store.UserHour{Hour: r.Hour, Heard: int(r.Heard), Fled: int(r.Fled), Triggered: int(r.Triggered)}
	}
	return out, nil
}

// RefreshAnalytics recounts the hourly rollups. One at a time rather than in a transaction: each is
// consistent on its own, and the views read past whatever either one is missing.
func (s *Store) RefreshAnalytics(ctx context.Context) error {
	if err := s.q.RefreshPlaysHourly(ctx); err != nil {
		return fmt.Errorf("refresh plays_hourly_mv: %w", err)
	}
	if err := s.q.RefreshListenersHourly(ctx); err != nil {
		return fmt.Errorf("refresh listeners_hourly_mv: %w", err)
	}
	return nil
}

//#endregion

//#region Helpers

func rows[T ~struct {
	Key string
	N   int32
}](in []T, err error) ([]store.Row, error) {
	if err != nil {
		return nil, err
	}
	out := make([]store.Row, len(in))
	for i, r := range in {
		v := struct {
			Key string
			N   int32
		}(r)
		out[i] = store.Row{Key: v.Key, N: int(v.N)}
	}
	return out, nil
}

// The database stores every Discord id as a bigint and every bounded setting as a smallint/int.
// These are the only places those conversions happen, so the justification lives here once.

// i64 converts a snowflake to the bigint column type. A snowflake is a 63-bit value (the sign bit
// is never set), so the round trip through int64 is exact for every id Discord can issue.
func i64(id snowflake.ID) int64 { return int64(id) } //nolint:gosec // snowflakes are 63-bit

// sid is the inverse of i64, for ids read back out of the database.
func sid(v int64) snowflake.ID { return snowflake.ID(v) } //nolint:gosec // round trip of i64

// small narrows a value the schema constrains to a small range (join_chance 0-100, suspense 0-20,
// fakeout and encore 0-50, listeners per play). Out-of-range input clamps rather than silently wrapping.
func small(v int) int16 {
	switch {
	case v > math.MaxInt16:
		return math.MaxInt16
	case v < math.MinInt16:
		return math.MinInt16
	}
	return int16(v)
}

// i32 narrows a count (member counts, day windows, durations) to the int column type, clamping at
// the edges for the same reason.
func i32(v int) int32 {
	switch {
	case v > math.MaxInt32:
		return math.MaxInt32
	case v < math.MinInt32:
		return math.MinInt32
	}
	return int32(v)
}

func idp(v *snowflake.ID) *int64 {
	if v == nil {
		return nil
	}
	i := i64(*v)
	return &i
}
func pi16(v *int) *int16 {
	if v == nil {
		return nil
	}
	i := small(*v)
	return &i
}
func i32p(v *int32) *int {
	if v == nil {
		return nil
	}
	i := int(*v)
	return &i
}

//#endregion

// deref is a nullable text column read back as its empty-string sentinel.
func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
