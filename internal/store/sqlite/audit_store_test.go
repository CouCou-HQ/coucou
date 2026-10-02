package sqlite

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/be-sandaa/coucou/internal/store"
)

// The backend is a file and a pure-Go driver, so this runs in the default suite: no container, no
// build tag, nothing to start.
func open(t *testing.T) (*Store, store.Store) {
	t.Helper()
	ctx := context.Background()
	s, err := Open(ctx, "sqlite://"+filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(s.Close)
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	ps, ok := s.(*Store)
	if !ok {
		t.Fatalf("open returned %T, want *Store", s)
	}
	return ps, s
}

type logRow struct {
	Schema, Table, Op, PkColumn string
	Pk                          int64
	By                          *int64
	Change                      map[string]map[string]any
}

func logs(t *testing.T, s *Store) []logRow {
	t.Helper()
	rows, err := s.db.QueryContext(context.Background(),
		`select schema_name, table_name, op, pk_column, pk, by, change from audit_logs order by id`)
	if err != nil {
		t.Fatalf("read audit_logs: %v", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			t.Errorf("close audit_logs rows: %v", err)
		}
	}()
	out := []logRow{}
	for rows.Next() {
		var r logRow
		var blob string
		if err := rows.Scan(&r.Schema, &r.Table, &r.Op, &r.PkColumn, &r.Pk, &r.By, &blob); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if err := json.Unmarshal([]byte(blob), &r.Change); err != nil {
			t.Fatalf("decode change %q: %v", blob, err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	return out
}

func id(v int64) *int64 { return &v }

func TestSettingsAreAudited(t *testing.T) {
	ps, s := open(t)
	ctx := context.Background()
	from, to := 22, 7

	for _, st := range []store.Settings{
		{Guild: 100, Chance: 5, TZ: new(tzBrussels)},                                                 // seeded by the bot
		{Guild: 100, Chance: 5, TZ: new(tzBrussels)},                                                 // the same row again
		{Guild: 100, Chance: 40, QuietFrom: &from, QuietTo: &to, TZ: new(tzBrussels), UpdatedBy: 42}, // a human
	} {
		if err := s.UpsertSettings(ctx, st); err != nil {
			t.Fatalf("upsert %+v: %v", st, err)
		}
	}

	// Two records, not three: rewriting a row with the values it already had is not a change. The
	// seed carries no quiet hours because those columns arrived null and so did not move.
	want := []logRow{
		{Schema: schemaGuilds, Table: tableSettings, Op: opInsert, PkColumn: colGuildID, Pk: 100, By: nil, Change: map[string]map[string]any{
			colJoinChance: moved(nil, float64(5)),
			colSuspense:   moved(nil, float64(0)),
			colFakeOut:    moved(nil, float64(0)),
			colEncore:     moved(nil, float64(0)),
			colTZ:         moved(nil, tzBrussels),
		}},
		{Schema: schemaGuilds, Table: tableSettings, Op: opUpdate, PkColumn: colGuildID, Pk: 100, By: id(42), Change: map[string]map[string]any{
			colJoinChance: moved(float64(5), float64(40)),
			colQuietFrom:  moved(nil, float64(22)),
			colQuietTo:    moved(nil, float64(7)),
		}},
	}
	if got := logs(t, ps); !reflect.DeepEqual(got, want) {
		t.Errorf("audit_logs =\n  %+v\nwant\n  %+v", got, want)
	}
}

func TestOptOutsAreAudited(t *testing.T) {
	ps, s := open(t)
	ctx := context.Background()

	// A real snowflake: more than 53 bits, so a log that decoded it as a float would round it.
	const user = 1234567890123456789
	until := time.Now().Add(time.Hour)

	if err := s.SetOptOut(ctx, store.OptOut{User: user}); err != nil {
		t.Fatalf("set: %v", err)
	}
	if err := s.SetOptOut(ctx, store.OptOut{User: user, Until: &until}); err != nil {
		t.Fatalf("extend: %v", err)
	}
	if err := s.ClearOptOut(ctx, user); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if err := s.ClearOptOut(ctx, 999); err != nil {
		t.Fatalf("clear an opt-out nobody had: %v", err)
	}

	// The columns carry timestamps, so the record is compared by everything but their values: which
	// row, which operation, who to credit, and which columns moved.
	type shape struct {
		Op      string
		Pk      int64
		By      *int64
		Columns []string
	}
	recorded := logs(t, ps)
	got := make([]shape, 0, len(recorded))
	for _, r := range recorded {
		cols := make([]string, 0, len(r.Change))
		for k := range r.Change {
			cols = append(cols, k)
		}
		sort.Strings(cols)
		got = append(got, shape{Op: r.Op, Pk: r.Pk, By: r.By, Columns: cols})
	}

	// Three records, not four: clearing an opt-out nobody had deleted nothing. An opt-out is always
	// set by the person it is about, so each one is credited to them.
	want := []shape{
		{Op: opInsert, Pk: user, By: id(user), Columns: []string{colSince}},
		{Op: opUpdate, Pk: user, By: id(user), Columns: []string{colUntil}},
		{Op: opDelete, Pk: user, By: id(user), Columns: []string{colSince, colUntil}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("audit_logs =\n  %+v\nwant\n  %+v", got, want)
	}
}
