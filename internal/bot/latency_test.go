package bot

import (
	"slices"
	"testing"
	"time"
)

// The gauge's contract is that zero means "not answering", so every way a shard can fail to answer
// has to fold to zero rather than to a plausible-looking duration.
func TestWorstLatency(t *testing.T) {
	ms := func(n ...int) []time.Duration {
		out := make([]time.Duration, len(n))
		for i, v := range n {
			out[i] = time.Duration(v) * time.Millisecond
		}
		return out
	}

	tests := []struct {
		name string
		in   []time.Duration
		want time.Duration
	}{
		{"single shard", ms(40), 40 * time.Millisecond},
		{"worst of several", ms(40, 120, 80), 120 * time.Millisecond},
		{"a shard awaiting its ack reads negative", ms(40, -5), 0},
		{"a shard that never heartbeat reads zero", ms(40, 0), 0},
		{"not connected yet", nil, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := worstLatency(slices.Values(tt.in)); got != tt.want {
				t.Errorf("worstLatency(%v) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}
