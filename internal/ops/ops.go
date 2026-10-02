// Package ops is the operational HTTP surface: the endpoints a scraper and an orchestrator ask
// about.
package ops

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/pprof"
	"time"

	"github.com/be-sandaa/coucou/internal/metrics"
	"github.com/be-sandaa/coucou/pkg/run"
)

const (
	// readyTimeout bounds one readiness check. It has to be shorter than the probe interval that
	// calls it, or a stuck database turns into a pile of in-flight requests instead of a 503.
	readyTimeout = 2 * time.Second
)

// Serve registers the endpoints on r. Register it before anything that produces numbers so it is
// torn down last and stays answerable to the end of a shutdown. A failure to listen is fatal: a
// bot nobody can scrape or probe is a bot nobody is watching.
//
//	/healthz  the process is not wedged: live returns nil. No dependencies — a liveness probe that
//	          checks them turns someone else's outage into a restart loop of your own, and a
//	          restart makes a gateway reconnect take longer, not shorter.
//	/readyz   the bot can do its job: ready returns nil.
//	/metrics  Prometheus.
//
// profiling adds /debug/pprof on the same listener. Off by default, so whether this port is
// trusted enough to expose profiles stays a per-deployment question rather than a compile-time one.
//
// An empty addr disables all three.
func Serve(r *run.Runner, addr string, profiling bool, live func() error, ready func(context.Context) error) {
	if addr == "" {
		slog.Info("ops endpoints disabled", slog.String("reason", "no listen address"))
		return
	}
	mux := newMux(profiling, addr, live, ready)

	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	r.Add(func(context.Context) error {
		slog.Info("ops endpoints listening", slog.String("addr", addr))
		if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}, srv.Shutdown)
}

// newMux builds the endpoint set. Split from Serve so a test can exercise the handlers without a
// listener, a port, or a Runner.
func newMux(profiling bool, addr string, live func() error, ready func(context.Context) error) *http.ServeMux {
	mux := http.NewServeMux()
	mux.Handle("/metrics", metrics.Handler())
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		if err := live(); err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		writeOK(w)
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, req *http.Request) {
		ctx, cancel := context.WithTimeout(req.Context(), readyTimeout)
		defer cancel()
		if err := ready(ctx); err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		writeOK(w)
	})

	if profiling {
		// net/http/pprof registers on DefaultServeMux in init(), which this is not, so the handlers
		// are wired by hand — importing the package exposes nothing on its own. No method prefixes:
		// go tool pprof POSTs to /symbol, and mirroring DefaultServeMux is what keeps it working.
		mux.HandleFunc("/debug/pprof/", pprof.Index)
		mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
		mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
		mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
		mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
		// Warn, not Info: /debug/pprof/profile costs 30 s of CPU sampling per request.
		slog.Warn("pprof endpoints enabled", slog.String("addr", addr))
	}

	return mux
}

// writeOK answers a passing probe. A failed write means the prober hung up mid-response, which is
// its problem rather than ours — but it is worth a line at debug rather than being discarded.
func writeOK(w http.ResponseWriter) {
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write([]byte("ok\n")); err != nil {
		slog.Debug("probe response", slog.Any("err", err))
	}
}
