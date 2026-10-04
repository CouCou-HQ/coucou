package sqlite

import (
	"encoding/json"
	"reflect"
	"testing"
)

// Column names as the tables spell them, and the one time zone the tests use. Named once so the
// linter's rule about repeated literals is satisfied by the thing that is actually schema.
const (
	colJoinChance = "join_chance"
	colQuietFrom  = "quiet_from"
	colQuietTo    = "quiet_to"
	colSuspense   = "suspense"
	colFakeOut    = "fakeout"
	colEncore     = "encore"
	colNSFW       = "nsfw"
	nsfwOn        = "on"
	nsfwOnly      = "restricted"
	colTZ         = "tz"
	colSince      = "since"
	colUntil      = "until"
	colRrule      = "rrule"
	tzBrussels    = "Europe/Brussels"
)

func num(s string) json.Number { return json.Number(s) }

// moved is one column's entry in a change: what it was and what it became.
func moved(from, to any) map[string]any { return map[string]any{keyFrom: from, keyTo: to} }

func TestRowMapKeepsSnowflakePrecision(t *testing.T) {
	// A snowflake is up to 63 bits; a JSON number decoded the default way is a float64, which holds
	// 53. These two ids differ by one and must not come back equal.
	type row struct {
		UserID int64 `json:"user_id"`
	}
	for _, id := range []int64{1234567890123456789, 1234567890123456788} {
		m, err := rowMap(row{UserID: id})
		if err != nil {
			t.Fatalf("rowMap(%d): %v", id, err)
		}
		got, ok := m[colUserID].(json.Number)
		if !ok {
			t.Fatalf("user_id is %T, want json.Number", m[colUserID])
		}
		if got.String() != "1234567890123456789" && got.String() != "1234567890123456788" {
			t.Errorf("user_id = %s, want the digits it was given", got)
		}
		if v, err := got.Int64(); err != nil || v != id {
			t.Errorf("user_id round trip = %d (%v), want %d", v, err, id)
		}
	}
}

func TestDiff(t *testing.T) {
	tests := []struct {
		name          string
		before, after map[string]any
		skip          []string
		want          map[string]any
	}{
		{
			name:  "insert reports every column that arrived",
			after: map[string]any{colGuildID: num("100"), colJoinChance: num("5"), colQuietFrom: nil},
			skip:  []string{colGuildID},
			want: map[string]any{
				colJoinChance: moved(nil, num("5")),
			},
		},
		{
			name:   "update reports only what moved",
			before: map[string]any{colJoinChance: num("5"), colTZ: tzBrussels, colSuspense: num("0")},
			after:  map[string]any{colJoinChance: num("40"), colTZ: tzBrussels, colSuspense: num("0")},
			want: map[string]any{
				colJoinChance: moved(num("5"), num("40")),
			},
		},
		{
			name:   "a rewrite with the same values is not a change",
			before: map[string]any{colJoinChance: num("5"), colTZ: tzBrussels},
			after:  map[string]any{colJoinChance: num("5"), colTZ: tzBrussels},
			want:   map[string]any{},
		},
		{
			name:   "only bookkeeping moved",
			before: map[string]any{colJoinChance: num("5"), colUpdatedAt: "before", colUpdatedBy: num("1")},
			after:  map[string]any{colJoinChance: num("5"), colUpdatedAt: "now", colUpdatedBy: num("2")},
			skip:   []string{colUpdatedAt, colUpdatedBy},
			want:   map[string]any{},
		},
		{
			name:   "a column cleared to null is a change",
			before: map[string]any{colQuietFrom: num("22")},
			after:  map[string]any{colQuietFrom: nil},
			want: map[string]any{
				colQuietFrom: moved(num("22"), nil),
			},
		},
		{
			name:   "null on both sides did not move",
			before: map[string]any{colRrule: nil},
			after:  map[string]any{colRrule: nil},
			want:   map[string]any{},
		},
		{
			name:   "delete reports every column that left",
			before: map[string]any{colUserID: num("7"), colSince: "a timestamp", colRrule: nil},
			skip:   []string{colUserID},
			want: map[string]any{
				colSince: moved("a timestamp", nil),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := diff(tt.before, tt.after, tt.skip...)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("diff =\n  %v\nwant\n  %v", got, tt.want)
			}
		})
	}
}

func TestActor(t *testing.T) {
	id := func(v int64) *int64 { return &v }
	tests := []struct {
		name          string
		before, after map[string]any
		want          *int64
	}{
		{
			name:  "the column the row keeps for it",
			after: map[string]any{colUpdatedBy: num("42"), colUserID: num("7")},
			want:  id(42),
		},
		{
			name:  "failing that, the row's own user",
			after: map[string]any{colUserID: num("7")},
			want:  id(7),
		},
		{
			name:   "a delete has only the row as it was",
			before: map[string]any{colUserID: num("7")},
			want:   id(7),
		},
		{
			name:  "zero is the bot acting on its own",
			after: map[string]any{colUpdatedBy: num("0")},
			want:  nil,
		},
		{
			name:  "no actor at all",
			after: map[string]any{colJoinChance: num("5")},
			want:  nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := actor(tt.before, tt.after)
			switch {
			case got == nil && tt.want == nil:
			case got == nil || tt.want == nil:
				t.Errorf("actor = %v, want %v", got, tt.want)
			case *got != *tt.want:
				t.Errorf("actor = %d, want %d", *got, *tt.want)
			}
		})
	}
}
