package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/be-sandaa/coucou/internal/store/sqlite/gen"
)

// The row-change log, which postgres gets from a trigger on the table and SQLite cannot: a trigger
// here has no way to ask a row what its columns are, so it would name them by hand and stop
// recording the one somebody adds next. Taken in Go instead, between reading the row and writing it,
// inside the transaction that does the write — so a failed write leaves no record of a change that
// did not happen.
//
// The price of doing it above the database rather than in it is that a hand-run UPDATE through the
// sqlite shell goes unrecorded. That is the objection to application-level auditing, and it is much
// weaker here than it would be in postgres: this backend exists for a single process holding one
// file, and the store is the only writer there is.

// The prefixes the audited tables carry, recorded as the schema the postgres backend keeps them in,
// so a log from either backend names the same table the same way.
const schemaGuilds = "guilds"

// The operations, spelled the way the audit_logs check constraint spells them.
const (
	opInsert = "insert"
	opUpdate = "update"
	opDelete = "delete"
)

// The two keys a change carries for each column that moved. The postgres trigger writes the same
// pair, so a reader of either log reaches for the same names.
const (
	keyFrom = "from"
	keyTo   = "to"
)

// The audited tables, named the way the postgres log names them — without the prefix the SQLite
// table carries, since the record has a column for the schema.
const tableSettings = "settings"

// Columns the record carries itself, and the one a row keeps for whoever it is about.
const (
	colUpdatedAt = "updated_at"
	colUpdatedBy = "updated_by"
	colUserID    = "user_id"
	colGuildID   = "guild_id"
)

// change is one row moving: what it was, what it became, and which row it is. Either side may carry
// no columns at all — an insert has nothing before it, a delete nothing after — and since no table
// has a row of zero columns, that is an unambiguous way to say the row was not there.
type change struct {
	schema   string
	table    string
	pkColumn string
	pk       int64
	before   map[string]any
	after    map[string]any
}

// audited reads the row, does the write, reads it back and records what moved — all in one
// transaction, so the record and the change it describes commit together or not at all, and a write
// that fails leaves no record of a change that did not happen. c carries which row this is; its
// before and after are filled in here.
func (s *Store) audited(
	ctx context.Context,
	c change,
	read func(*gen.Queries) (map[string]any, error),
	write func(*gen.Queries) error,
) error {
	return s.tx(ctx, func(q *gen.Queries) error {
		before, err := read(q)
		if err != nil {
			return err
		}
		if err := write(q); err != nil {
			return err
		}
		after, err := read(q)
		if err != nil {
			return err
		}
		c.before, c.after = before, after
		return record(ctx, q, c)
	})
}

// record writes one row-change record, or nothing at all when nothing moved. A write that rewrites a
// row with the values it already had is not somebody changing it, which is the same rule the
// postgres trigger applies before it inserts.
func record(ctx context.Context, q *gen.Queries, c change) error {
	// The key column, updated_at and updated_by are left out because the record already carries all
	// three, as pk, at and by.
	moved := diff(c.before, c.after, c.pkColumn, colUpdatedAt, colUpdatedBy)
	if len(moved) == 0 {
		return nil
	}
	blob, err := json.Marshal(moved)
	if err != nil {
		return fmt.Errorf("audit: encode the change: %w", err)
	}

	op := opUpdate
	switch {
	case len(c.before) == 0:
		op = opInsert
	case len(c.after) == 0:
		op = opDelete
	}
	return q.InsertAuditLog(ctx, gen.InsertAuditLogParams{
		SchemaName: c.schema, TableName: c.table, Op: op,
		PkColumn: c.pkColumn, Pk: c.pk, By: actor(c.before, c.after), Change: string(blob),
	})
}

// getRow is the row as it stands, decoded into its columns, or no columns at all when there is no
// such row. Generic so the audited tables share one call shape — getRow(q.GetX(ctx, id)).
func getRow[T any](row T, err error) (map[string]any, error) {
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return map[string]any{}, nil
	case err != nil:
		return nil, err
	}
	return rowMap(row)
}

// rowMap decodes a generated row struct into its columns. The keys are column names because sqlc
// tags the fields with them, which is what makes this generic: the diff is over whatever the row
// has, not over a list of columns written out somewhere that has to be kept in step.
//
// UseNumber is not optional. A snowflake is up to 63 bits and the default decoding of a JSON number
// is float64, which carries 53 — every id in the log would come back rounded, and two different
// users would compare equal.
func rowMap(v any) (map[string]any, error) {
	blob, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(blob))
	dec.UseNumber()
	var m map[string]any
	if err := dec.Decode(&m); err != nil {
		return nil, err
	}
	return m, nil
}

// diff reports the columns that moved, as {column: {"from": was, "to": became}}. A column missing
// from one side reads as null there, so an insert is every column arriving from nothing and a delete
// is every column leaving; a column that was null and stayed null did not move.
func diff(before, after map[string]any, skip ...string) map[string]any {
	skipped := make(map[string]bool, len(skip))
	for _, k := range skip {
		skipped[k] = true
	}
	out := map[string]any{}
	for _, k := range union(before, after) {
		if skipped[k] {
			continue
		}
		was, became := before[k], after[k]
		if same(was, became) {
			continue
		}
		out[k] = map[string]any{keyFrom: was, keyTo: became}
	}
	return out
}

func union(a, b map[string]any) []string {
	keys := make([]string, 0, len(a)+len(b))
	seen := make(map[string]bool, len(a)+len(b))
	for _, m := range []map[string]any{a, b} {
		for k := range m {
			if !seen[k] {
				seen[k], keys = true, append(keys, k)
			}
		}
	}
	return keys
}

// same compares two decoded column values. Numbers are json.Number, so this is a comparison of the
// digits as written rather than of two floats that may both be wrong.
func same(a, b any) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a == b
}

// actor is who to credit: the column a row keeps for it, or failing that the row's own user, since
// an opt-out is always set by the person it is about. The same rule the postgres trigger uses.
func actor(before, after map[string]any) *int64 {
	for _, m := range []map[string]any{after, before} {
		for _, k := range []string{colUpdatedBy, colUserID} {
			if v, ok := m[k].(json.Number); ok {
				if id, err := strconv.ParseInt(v.String(), 10, 64); err == nil && id != 0 {
					return &id
				}
			}
		}
	}
	return nil
}
