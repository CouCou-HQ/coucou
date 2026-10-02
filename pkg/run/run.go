// Package run starts a set of processes and stops them gracefully on an OS signal or the first
// process failure.
//
// A Runner is built with New (which chooses the shutdown signals), processes are registered with
// Add as a start/stop pair, and Run blocks until shutdown.
//
// It deliberately has no errgroup dependency: cancel-on-first-error over a handful of goroutines
// is a WaitGroup, a sync.Once and a context.
package run

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

// gracePeriod bounds graceful shutdown. 10s leaves headroom inside the usual 30s a supervisor
// allows between SIGTERM and SIGKILL.
const gracePeriod = 10 * time.Second

// Func is one phase of a process — a start or a stop.
type Func func(context.Context) error

// NoOp is a Func that does nothing — for a process with only a start or only a stop phase.
func NoOp(context.Context) error { return nil }

// NoCtx adapts a context-less func into a Func.
func NoCtx(fn func() error) Func {
	return func(context.Context) error { return fn() }
}

// NoErr adapts a func() with neither context nor error into a Func — for cleanup like Store.Close.
func NoErr(fn func()) Func {
	return func(context.Context) error { fn(); return nil }
}

// Ctx adapts a func(context.Context) with no error into a Func.
func Ctx(fn func(context.Context)) Func {
	return func(ctx context.Context) error { fn(ctx); return nil }
}

type process struct{ start, stop Func }

// Runner starts processes concurrently and stops them in reverse registration order.
//
// It embeds a signal-bound context.Context — its root lifecycle context, cancelled on shutdown — so
// *Runner is usable anywhere a context.Context is.
type Runner struct {
	context.Context

	stop      context.CancelFunc
	processes []process
}

var _ context.Context = (*Runner)(nil)

// New returns a Runner whose context is cancelled on the given signals, defaulting to SIGINT and
// SIGTERM when none are passed.
func New(signals ...os.Signal) *Runner {
	if len(signals) == 0 {
		signals = []os.Signal{syscall.SIGINT, syscall.SIGTERM}
	}
	ctx, stop := signal.NotifyContext(context.Background(), signals...)
	return &Runner{Context: ctx, stop: stop}
}

// Stop releases the signal handler without running the shutdown sequence. Use it on an early exit
// that happens before Run — a failed migration, say. Run calls it itself.
func (r *Runner) Stop() { r.stop() }

// Add registers a process by its start and stop funcs. Either may be nil (skipped). start runs
// concurrently; stop runs at shutdown in reverse registration order, so register a dependency
// before the things that use it.
func (r *Runner) Add(start, stop Func) {
	r.processes = append(r.processes, process{start: start, stop: stop})
}

// KeepAlive returns a start/stop pair that blocks until the runner shuts down. Spread it into Add —
// r.Add(run.KeepAlive()) — to make a runner long-lived: without it Run returns as soon as every
// start has returned, which for a bot whose starts are all non-blocking would be immediately.
func KeepAlive() (start, stop Func) {
	return func(ctx context.Context) error { <-ctx.Done(); return nil }, nil
}

// Run starts every process and blocks until the Runner's context is cancelled (a signal), a start
// func returns a non-nil error, or every start func has returned; then stops the processes in
// reverse order within the grace period. It returns the first start error joined with any stop
// errors; a signal or context cancellation is a clean shutdown, not an error.
func (r *Runner) Run() error { //nolint:gocyclo // start loop, wake-up select and reverse stop loop are one sequence
	defer r.stop()

	ctx, cancel := context.WithCancel(r.Context)
	defer cancel()

	var (
		wg       sync.WaitGroup
		once     sync.Once
		startErr error
	)
	for _, p := range r.processes {
		if p.start == nil {
			continue
		}
		start := p.start
		wg.Go(func() {
			if err := start(ctx); err != nil {
				// First error wins and brings the rest down with it.
				once.Do(func() { startErr = err })
				cancel()
			}
		})
	}

	// Wake on a signal or a start error (ctx cancels), or once every start has returned. Waiting
	// for *all* starts rather than the first means a non-blocking start returning early does not
	// tear the runner down while a sibling still blocks.
	allDone := make(chan struct{})
	go func() { wg.Wait(); close(allDone) }()
	select {
	case <-ctx.Done():
	case <-allDone:
	}

	// Stops get a fresh budget: ctx is already cancelled and must not leak into shutdown.
	// ponytail: one shared budget, sequential reverse-order stops, trusted to honour the context.
	// A stop that ignores it can overrun — SIGKILL is the backstop. Wrap each stop in its own
	// goroutine only if a real hang ever shows up.
	stopCtx, stopCancel := context.WithTimeout(context.WithoutCancel(r.Context), gracePeriod)
	defer stopCancel()

	var errs []error
	for i := len(r.processes) - 1; i >= 0; i-- {
		if s := r.processes[i].stop; s != nil {
			if err := s(stopCtx); err != nil {
				errs = append(errs, err)
			}
		}
	}

	// Drain the start goroutines before reporting, so startErr is settled.
	wg.Wait()
	if startErr != nil && !errors.Is(startErr, context.Canceled) {
		errs = append(errs, startErr)
	}
	return errors.Join(errs...)
}
