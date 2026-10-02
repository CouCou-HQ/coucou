package validate_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/be-sandaa/coucou/internal/validate"
)

type sample struct {
	ID       uint64        `json:"id" validate:"required"`
	Name     string        `json:"name,omitempty" validate:"required"`
	Count    int           `json:"member_count" validate:"min=0"`
	Rank     uint32        `json:"rank" validate:"min=1"`
	Dur      time.Duration `json:"dur" validate:"min=0"`
	At       time.Time     `json:"at" validate:"required"`
	Untagged string        `json:"untagged"`
	NoJSON   int           `validate:"min=0"`
}

const (
	errIDRequired   = "id: is required"
	errNameRequired = "name: is required"
)

func valid() sample {
	return sample{ID: 1, Name: "x", Count: 0, Rank: 1, Dur: 0, At: time.Now()}
}

func TestValidStructPasses(t *testing.T) {
	if err := validate.Struct(valid()); err != nil {
		t.Fatalf("Struct(valid) = %v, want nil", err)
	}
	v := valid()
	if err := validate.Struct(&v); err != nil {
		t.Fatalf("Struct(&valid) = %v, want nil — a pointer must work too", err)
	}
}

func TestRulesReportTheJSONName(t *testing.T) {
	for _, tc := range []struct {
		name  string
		mut   func(*sample)
		wants string
	}{
		{"zero id", func(s *sample) { s.ID = 0 }, errIDRequired},
		{"empty name drops omitempty", func(s *sample) { s.Name = "" }, errNameRequired},
		{"zero time", func(s *sample) { s.At = time.Time{} }, "at: is required"},
		{"negative int", func(s *sample) { s.Count = -3 }, "member_count: must be at least 0, got -3"},
		{"negative duration", func(s *sample) { s.Dur = -time.Second }, "dur: must be at least 0"},
		{"uint below min", func(s *sample) { s.Rank = 0 }, "rank: must be at least 1, got 0"},
		{"no json tag falls back", func(s *sample) { s.NoJSON = -1 }, "NoJSON: must be at least 0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := valid()
			tc.mut(&s)
			err := validate.Struct(s)
			if err == nil {
				t.Fatalf("Struct(%+v) = nil, want an error", s)
			}
			if !strings.Contains(err.Error(), tc.wants) {
				t.Errorf("error = %q, want it to contain %q", err, tc.wants)
			}
		})
	}
}

// A caller fixing a payload wants every problem at once, not one per round trip.
func TestAllBrokenRulesAreReported(t *testing.T) {
	s := valid()
	s.ID, s.Name, s.Count = 0, "", -1
	err := validate.Struct(s)
	if err == nil {
		t.Fatal("Struct = nil, want three errors")
	}
	for _, want := range []string{errIDRequired, errNameRequired, "member_count: must be at least 0"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, missing %q", err, want)
		}
	}
}

// A tag the library does not know panics rather than returning an error — "nonzero" is what
// someone arriving from another validator library would reach for. Pinned because it is the one
// way this package can take a process down, and because a silent no-op would be worse: a typo
// would switch validation off for that field with nothing to say so.
func TestUnknownRuleIsAPanic(t *testing.T) {
	type borrowed struct {
		ID uint64 `json:"id" validate:"nonzero"`
	}
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("Struct did not panic on an unregistered tag")
		}
		if !strings.Contains(fmt.Sprint(r), "nonzero") {
			t.Errorf("panic = %v, want it to name the unknown tag", r)
		}
	}()
	if err := validate.Struct(borrowed{ID: 1}); err != nil {
		t.Fatalf("Struct returned %v; the unregistered tag should have panicked before this", err)
	}
}

func TestNonStructIsAnError(t *testing.T) {
	if err := validate.Struct("not a struct"); err == nil {
		t.Fatal("Struct(string) = nil, want an error")
	}
}

// An untagged struct is not an error — most types have no rules and must pass untouched.
func TestUntaggedStructPasses(t *testing.T) {
	type plain struct{ A, B string }
	if err := validate.Struct(plain{}); err != nil {
		t.Fatalf("Struct(plain{}) = %v, want nil", err)
	}
}
