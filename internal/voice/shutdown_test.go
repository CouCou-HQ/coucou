package voice

import (
	"context"
	"testing"
	"time"

	"github.com/disgoorg/snowflake/v2"
)

// fakePlay stands in for Play's busy entry: it leaves the map only once cancelled, after linger.
func fakePlay(t *testing.T, guild snowflake.ID, linger time.Duration) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	busy.Store(guild, cancel)
	t.Cleanup(func() { busy.Delete(guild) })
	go func() {
		<-ctx.Done()
		time.Sleep(linger)
		busy.Delete(guild)
	}()
}

func TestShutdown(t *testing.T) {
	tests := []struct {
		name    string
		linger  time.Duration
		budget  time.Duration
		waitMin time.Duration
		left    bool
	}{
		{name: "waits for every play to leave", linger: 100 * time.Millisecond, budget: 5 * time.Second, waitMin: 100 * time.Millisecond, left: true},
		{name: "gives up when the budget runs out", linger: time.Hour, budget: 100 * time.Millisecond, left: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakePlay(t, 1, tt.linger)
			fakePlay(t, 2, tt.linger)

			ctx, cancel := context.WithTimeout(context.Background(), tt.budget)
			defer cancel()
			start := time.Now()
			Shutdown(ctx)

			if took := time.Since(start); took < tt.waitMin || took > tt.budget+time.Second {
				t.Errorf("Shutdown took %s; want between %s and %s", took, tt.waitMin, tt.budget)
			}
			if gone := !Busy(1) && !Busy(2); gone != tt.left {
				t.Errorf("plays left = %v, want %v", gone, tt.left)
			}
		})
	}
}
