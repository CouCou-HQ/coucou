package bot

import (
	"context"
	"testing"
	"time"

	"github.com/disgoorg/snowflake/v2"
)

// The pre-burst empty set is the whole bug: at open no READY has been handled yet, so the unready
// set is empty for the same reason it is empty when the burst is done.
func TestWaitForGuildsIgnoresThePreBurstEmptySet(t *testing.T) {
	var calls int
	readied := func() int {
		calls++
		if calls == 1 {
			return 0 // READY not handled yet
		}
		return 1
	}
	unready, err := waitForGuilds(context.Background(), 1, readied, func() []snowflake.ID { return nil })
	if err != nil {
		t.Fatalf("waitForGuilds: %v", err)
	}
	if len(unready) != 0 {
		t.Errorf("unready = %v, want none", unready)
	}
	if calls < 2 {
		t.Errorf("returned after %d readiness checks; it took the pre-burst empty set for a drained one", calls)
	}
}

func TestWaitForGuilds(t *testing.T) {
	a, b := snowflake.ID(1), snowflake.ID(2)
	tests := []struct {
		name    string
		shards  int
		readied int
		unready []snowflake.ID
		want    []snowflake.ID
		wantErr bool
	}{
		{name: "settled", shards: 1, readied: 1},
		// Stragglers come back as a keep-list, not as a failure: two unavailable guilds must not
		// cost the others their reconcile.
		{name: "stragglers", shards: 2, readied: 2, unready: []snowflake.ID{a, b}, want: []snowflake.ID{a, b}},
		{name: "silent shard", shards: 2, readied: 1, wantErr: true},
		{name: "no shards", shards: 0, readied: 0, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
			defer cancel()
			got, err := waitForGuilds(ctx, tt.shards, func() int { return tt.readied }, func() []snowflake.ID { return tt.unready })
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("unready = %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("unready[%d] = %d, want %d", i, got[i], tt.want[i])
				}
			}
		})
	}
}
