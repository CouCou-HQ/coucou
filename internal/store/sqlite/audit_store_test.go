package sqlite

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"

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

	for _, st := range []store.Settings{
		{Guild: 100, Chance: 5, TZ: new(tzBrussels), NSFW: nsfwOnly},                              // seeded by the bot
		{Guild: 100, Chance: 5, TZ: new(tzBrussels), NSFW: nsfwOnly},                              // the same row again
		{Guild: 100, Chance: 40, Suspense: 3, TZ: new(tzBrussels), NSFW: nsfwOnly, UpdatedBy: 42}, // a human
		{Guild: 100, Chance: 40, Suspense: 3, TZ: new(tzBrussels), NSFW: nsfwOn, UpdatedBy: 43},   // /nsfw on, by whom
	} {
		if err := s.UpsertSettings(ctx, st); err != nil {
			t.Fatalf("upsert %+v: %v", st, err)
		}
	}

	// Three records, not four: rewriting a row with the values it already had is not a change. The
	// last is who turned 18+ sounds on everywhere, which is the record /nsfw on owes the server.
	want := []logRow{
		{Schema: schemaGuilds, Table: tableSettings, Op: opInsert, PkColumn: colGuildID, Pk: 100, By: nil, Change: map[string]map[string]any{
			colJoinChance: moved(nil, float64(5)),
			colSuspense:   moved(nil, float64(0)),
			colFakeOut:    moved(nil, float64(0)),
			colEncore:     moved(nil, float64(0)),
			colNSFW:       moved(nil, nsfwOnly),
			colTZ:         moved(nil, tzBrussels),
		}},
		{Schema: schemaGuilds, Table: tableSettings, Op: opUpdate, PkColumn: colGuildID, Pk: 100, By: id(42), Change: map[string]map[string]any{
			colJoinChance: moved(float64(5), float64(40)),
			colSuspense:   moved(float64(0), float64(3)),
		}},
		{Schema: schemaGuilds, Table: tableSettings, Op: opUpdate, PkColumn: colGuildID, Pk: 100, By: id(43), Change: map[string]map[string]any{
			colNSFW: moved(nsfwOnly, nsfwOn),
		}},
	}
	if got := logs(t, ps); !reflect.DeepEqual(got, want) {
		t.Errorf("audit_logs =\n  %+v\nwant\n  %+v", got, want)
	}
}
