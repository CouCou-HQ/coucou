package sqlite

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/pressly/goose/v3"
)

// 00013 rebuilds stats_plays to widen its trigger check. Dropping the old table with foreign keys on
// would cascade into stats_play_listeners, so the rows written before it must still be there after,
// and the cascade must still work once the swap is done.
func TestEncoreRebuildKeepsListeners(t *testing.T) {
	ctx := context.Background()
	s := migratedTo(t, 12)
	exec(t, s, `insert into stats_plays (id, at, guild_id, channel_id, sound, trigger, listeners, ok, duration_ms) values (1, '2026-01-01T00:00:00Z', 1, 2, 'a', 'loop', 1, 1, 10)`)
	exec(t, s, `insert into stats_play_listeners (play_id, user_id) values (1, 3)`)
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	if n := listeners(t, s); n != 1 {
		t.Fatalf("%d listener rows after the rebuild, want 1", n)
	}
	exec(t, s, `insert into stats_plays (at, guild_id, channel_id, sound, trigger, listeners, ok, duration_ms) values ('2026-01-01T00:00:00Z', 1, 2, 'b', 'encore', 0, 1, 10)`)
	exec(t, s, `delete from stats_plays where id = 1`)
	if n := listeners(t, s); n != 0 {
		t.Errorf("%d listener rows after deleting their play, want 0: the cascade did not survive", n)
	}
}

func migratedTo(t *testing.T, version int64) *Store {
	t.Helper()
	st, err := Open(context.Background(), "sqlite://"+filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(st.Close)
	s, ok := st.(*Store)
	if !ok {
		t.Fatalf("open returned %T, want *Store", st)
	}
	goose.SetBaseFS(migrations)
	goose.SetTableName(gooseTable)
	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatal(err)
	}
	if err := goose.UpToContext(context.Background(), s.db, "migrations", version); err != nil {
		t.Fatalf("migrate to %d: %v", version, err)
	}
	return s
}

func exec(t *testing.T, s *Store, q string) {
	t.Helper()
	if _, err := s.db.ExecContext(context.Background(), q); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

func listeners(t *testing.T, s *Store) int {
	t.Helper()
	var n int
	if err := s.db.QueryRowContext(context.Background(), `select count(*) from stats_play_listeners`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}
