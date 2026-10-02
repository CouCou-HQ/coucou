package run

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"
)

// newTestRunner builds a Runner bound to a context we control instead of to real OS signals.
func newTestRunner(ctx context.Context) *Runner {
	c, cancel := context.WithCancel(ctx)
	return &Runner{Context: c, stop: cancel}
}

func TestStopsRunInReverseRegistrationOrder(t *testing.T) {
	var mu sync.Mutex
	var order []string
	note := func(s string) Func {
		return func(context.Context) error {
			mu.Lock()
			order = append(order, s)
			mu.Unlock()
			return nil
		}
	}

	r := newTestRunner(context.Background())
	r.Add(nil, note("db"))
	r.Add(nil, note("bus"))
	r.Add(nil, note("gateway"))

	if err := r.Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := []string{"gateway", "bus", "db"}
	if !slices.Equal(order, want) {
		t.Errorf("stop order = %v, want %v — a dependency must outlive its users", order, want)
	}
}

func TestRunReturnsWhenEveryStartHasReturned(t *testing.T) {
	r := newTestRunner(context.Background())
	var mu sync.Mutex
	ran := 0
	count := func(context.Context) error {
		mu.Lock()
		ran++
		mu.Unlock()
		return nil
	}
	r.Add(count, nil)
	r.Add(count, nil)

	done := make(chan error, 1)
	go func() { done <- r.Run() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run hung after every start returned")
	}
	mu.Lock()
	defer mu.Unlock()
	if ran != 2 {
		t.Errorf("ran %d starts, want 2", ran)
	}
}

func TestKeepAliveHoldsTheRunnerOpen(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	r := newTestRunner(ctx)
	r.Add(KeepAlive())
	r.Add(func(context.Context) error { return nil }, nil) // returns at once, must not end the run

	done := make(chan error, 1)
	go func() { done <- r.Run() }()

	select {
	case <-done:
		t.Fatal("Run returned even though KeepAlive was registered")
	case <-time.After(200 * time.Millisecond):
	}

	cancel() // the signal stand-in
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("a cancelled context is a clean shutdown, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancellation")
	}
}

func TestStartErrorShutsEverythingDownAndIsReturned(t *testing.T) {
	boom := errors.New("bind failed")
	r := newTestRunner(context.Background())

	stopped := make(chan struct{})
	r.Add(KeepAlive())
	r.Add(func(context.Context) error { return boom }, func(context.Context) error {
		close(stopped)
		return nil
	})

	done := make(chan error, 1)
	go func() { done <- r.Run() }()

	select {
	case err := <-done:
		if !errors.Is(err, boom) {
			t.Fatalf("Run = %v, want it to carry %v", err, boom)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a failing start did not bring the runner down")
	}
	select {
	case <-stopped:
	default:
		t.Error("stops did not run after a start failed")
	}
}

func TestContextCancellationIsNotAnError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	r := newTestRunner(ctx)
	// A start that returns ctx.Err() on shutdown is the normal shape; it must not look like a failure.
	r.Add(func(c context.Context) error { <-c.Done(); return c.Err() }, nil)

	done := make(chan error, 1)
	go func() { done <- r.Run() }()
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run = %v, want nil for a clean shutdown", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return")
	}
}

func TestStopErrorsAreReported(t *testing.T) {
	boom := errors.New("flush failed")
	r := newTestRunner(context.Background())
	r.Add(nil, func(context.Context) error { return boom })

	if err := r.Run(); !errors.Is(err, boom) {
		t.Fatalf("Run = %v, want it to carry %v", err, boom)
	}
}

func TestStopsGetALiveContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	r := newTestRunner(ctx)

	var stopErr error
	r.Add(KeepAlive())
	r.Add(nil, func(c context.Context) error { stopErr = c.Err(); return nil })

	done := make(chan error, 1)
	go func() { done <- r.Run() }()
	cancel()
	<-done

	// The run context is already cancelled by now; stops must get a fresh budget instead, or every
	// graceful shutdown would abort immediately.
	if stopErr != nil {
		t.Errorf("stop context was already cancelled (%v); it must carry its own deadline", stopErr)
	}
}

func TestAdapters(t *testing.T) {
	ctx := context.Background()
	if err := NoOp(ctx); err != nil {
		t.Errorf("NoOp: %v", err)
	}

	boom := errors.New("nope")
	if err := NoCtx(func() error { return boom })(ctx); !errors.Is(err, boom) {
		t.Errorf("NoCtx did not pass the error through: %v", err)
	}

	called := false
	if err := NoErr(func() { called = true })(ctx); err != nil || !called {
		t.Errorf("NoErr: called=%v err=%v", called, err)
	}

	got := false
	if err := Ctx(func(context.Context) { got = true })(ctx); err != nil || !got {
		t.Errorf("Ctx: called=%v err=%v", got, err)
	}
}
