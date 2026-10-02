package ops

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

const symbolPath = "/debug/pprof/symbol"

func okReady(context.Context) error { return nil }

// pprof stays off unless a deployment asks for it: the endpoints expose allocation sites and
// goroutine stacks, and /debug/pprof/profile costs 30 s of CPU sampling per request. Absent by
// default is what keeps "is this port trusted" a deployment question.
func TestPProfEndpointsFollowTheFlag(t *testing.T) {
	// Index and cmdline answer immediately. profile and trace are deliberately not requested here.
	paths := []string{"/debug/pprof/", "/debug/pprof/cmdline"}

	tests := []struct {
		name      string
		profiling bool
		want      int
	}{
		{"off by default", false, http.StatusNotFound},
		{"on when asked", true, http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mux := newMux(tt.profiling, ":0", alive, okReady)
			for _, p := range paths {
				rec := httptest.NewRecorder()
				mux.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, p, nil))
				if rec.Code != tt.want {
					t.Errorf("GET %s = %d, want %d", p, rec.Code, tt.want)
				}
			}
		})
	}
}

// go tool pprof POSTs to /symbol, so restricting these to GET would break symbolisation.
func TestPProfSymbolAcceptsPost(t *testing.T) {
	mux := newMux(true, ":0", alive, okReady)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodPost, symbolPath, nil))
	if rec.Code != http.StatusOK {
		t.Errorf("POST /debug/pprof/symbol = %d, want %d", rec.Code, http.StatusOK)
	}
}
