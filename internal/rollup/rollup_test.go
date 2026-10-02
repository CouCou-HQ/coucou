package rollup

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/be-sandaa/coucou/internal/store"
)

type fakeStore struct {
	store.Store
	calls atomic.Int32
	err   error
}

func (f *fakeStore) RefreshAnalytics(context.Context) error {
	f.calls.Add(1)
	return f.err
}

func TestRunRefreshesAtOnceAndStopsWithItsContext(t *testing.T) {
	db := &fakeStore{err: errors.New("down")}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error)
	go func() { done <- New(db).Run(ctx) }()

	deadline := time.Now().Add(time.Second)
	for db.calls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if db.calls.Load() == 0 {
		t.Fatal("Run did not refresh before its first tick")
	}
	cancel()
	// A failing refresh is logged, not returned: Run only ends with its context.
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Errorf("Run = %v, want context.Canceled", err)
	}
}
