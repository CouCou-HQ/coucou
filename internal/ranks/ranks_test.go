package ranks

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/be-sandaa/coucou/internal/store"
)

// cutsOf builds what the store returns: cut k is the value at sorted position ceil(k*N/100).
func cutsOf(sorted ...float64) []float64 {
	n := len(sorted)
	out := make([]float64, 100)
	for k := 1; k <= 100; k++ {
		out[k-1] = sorted[(k*n+99)/100-1]
	}
	return out
}

func TestBelowIsTheStrictlyLessShare(t *testing.T) {
	tests := []struct {
		name string
		cuts []float64
		v    float64
		want int
	}{
		{"under everyone", cutsOf(1, 2, 3, 4), 0, 0},
		{"the minimum beats nobody", cutsOf(1, 2, 3, 4), 1, 0},
		{"the maximum beats the rest", cutsOf(1, 2, 3, 4), 4, 75},
		{"above everyone", cutsOf(1, 2, 3, 4), 9, 100},
		{"a tie is not beaten", cutsOf(1, 1, 1, 2), 1, 0},
		{"above a tie beats all of it", cutsOf(1, 1, 1, 2), 2, 75},
		{"everyone tied", cutsOf(5, 5, 5), 5, 0},
		{"between two values", cutsOf(1, 3), 2, 50},
		{"one person", cutsOf(7), 7, 0},
		{"floors to the whole percent", cutsOf(1, 2, 3), 3, 66},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := below(tt.cuts, tt.v); got != tt.want {
				t.Errorf("below(%v) = %d, want %d", tt.v, got, tt.want)
			}
		})
	}
}

type fakeStore struct {
	store.Store
	cuts map[string][]float64
	err  error
}

func (f *fakeStore) Cuts(_ context.Context, metric string, _, _ time.Time) ([]float64, error) {
	return f.cuts[metric], f.err
}

func TestBelowWithoutCutPoints(t *testing.T) {
	db := &fakeStore{cuts: map[string][]float64{store.MetricHeard: cutsOf(1, 2)}}
	c := New(db)
	if _, ok := c.Below(store.MetricHeard, 2); ok {
		t.Fatal("Below before the first refresh reported a rank")
	}
	c.refresh(context.Background(), time.Now())
	if got, ok := c.Below(store.MetricHeard, 2); !ok || got != 50 {
		t.Errorf("Below(heard, 2) = %d, %v; want 50, true", got, ok)
	}
	if _, ok := c.Below(store.MetricPlays, 2); ok {
		t.Error("an empty population reported a rank")
	}

	db.err = errors.New("down")
	c.refresh(context.Background(), time.Now())
	if _, ok := c.Below(store.MetricHeard, 2); !ok {
		t.Error("a failed refresh dropped the cut-points it already had")
	}
}
