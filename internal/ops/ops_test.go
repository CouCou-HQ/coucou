package ops

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

var (
	errNotReady = errors.New("database is asleep")
	errWedged   = errors.New("no gateway heartbeat ACK for 3m0s")
)

func alive() error { return nil }

func get(t *testing.T, mux *http.ServeMux, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil))
	return rec
}

// The distinction the package is built around, and the one a later edit could quietly invert:
// liveness checks only the process itself, readiness checks the world. A liveness probe that
// consulted the database would turn someone else's outage into a restart loop of our own.
func TestProbesCheckDifferentThings(t *testing.T) {
	tests := []struct {
		name     string
		live     func() error
		ready    func(context.Context) error
		healthz  int
		readyz   int
		readyHas string
	}{
		{"everything up", alive, func(context.Context) error { return nil }, http.StatusOK, http.StatusOK, ""},
		{
			name:     "database down",
			live:     alive,
			ready:    func(context.Context) error { return errNotReady },
			healthz:  http.StatusOK, // the point: liveness does not care
			readyz:   http.StatusServiceUnavailable,
			readyHas: errNotReady.Error(),
		},
		{
			name:    "gateway dispatch wedged",
			live:    func() error { return errWedged },
			ready:   func(context.Context) error { return nil },
			healthz: http.StatusServiceUnavailable,
			readyz:  http.StatusOK,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mux := newMux(false, ":0", tt.live, tt.ready)

			if got := get(t, mux, "/healthz").Code; got != tt.healthz {
				t.Errorf("GET /healthz = %d, want %d", got, tt.healthz)
			}
			rec := get(t, mux, "/readyz")
			if rec.Code != tt.readyz {
				t.Errorf("GET /readyz = %d, want %d", rec.Code, tt.readyz)
			}
			// The body carries the reason; a 503 that says nothing is a 503 nobody can act on.
			if tt.readyHas != "" && !strings.Contains(rec.Body.String(), tt.readyHas) {
				t.Errorf("GET /readyz body = %q, want it to mention %q", rec.Body.String(), tt.readyHas)
			}
		})
	}
}

// readyTimeout has to reach the check itself, not just wrap the handler: a ready that ignores its
// context can still hang, but one that honours it must be told when to give up.
func TestReadyzGivesItsCheckADeadline(t *testing.T) {
	var deadline time.Duration
	mux := newMux(false, ":0", alive, func(ctx context.Context) error {
		if dl, ok := ctx.Deadline(); ok {
			deadline = time.Until(dl)
		}
		return nil
	})

	get(t, mux, "/readyz")
	if deadline <= 0 || deadline > readyTimeout {
		t.Errorf("ready was given %v, want a positive deadline no longer than %v", deadline, readyTimeout)
	}
}

// A stuck database has to become a 503 rather than a request that never answers, or the probes
// pile up behind it and the orchestrator learns nothing.
func TestReadyzAnswers503WhenTheCheckBlocks(t *testing.T) {
	mux := newMux(false, ":0", alive, func(ctx context.Context) error {
		<-ctx.Done() // what a query against an unreachable database looks like
		return ctx.Err()
	})

	done := make(chan int, 1)
	go func() { done <- get(t, mux, "/readyz").Code }()
	select {
	case code := <-done:
		if code != http.StatusServiceUnavailable {
			t.Errorf("GET /readyz = %d, want %d", code, http.StatusServiceUnavailable)
		}
	case <-time.After(readyTimeout + 5*time.Second):
		t.Fatalf("GET /readyz never answered; readyTimeout of %v did not bound the check", readyTimeout)
	}
}

// An empty addr disables the surface entirely, which means returning before the Runner is touched.
// A nil Runner proves that without standing one up: registering anything would panic on it.
func TestServeWithoutAnAddressRegistersNothing(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Serve reached the Runner for an empty addr: %v", r)
		}
	}()
	Serve(nil, "", false, alive, func(context.Context) error { return nil })
}
