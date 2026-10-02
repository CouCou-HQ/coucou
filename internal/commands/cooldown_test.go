package commands

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/disgoorg/snowflake/v2"
)

func TestCooldownTake(t *testing.T) {
	const (
		g1, g2 = snowflake.ID(1), snowflake.ID(2)
		u1, u2 = snowflake.ID(10), snowflake.ID(20)
	)
	type play struct {
		at       time.Duration
		guild    snowflake.ID
		user     snowflake.ID
		wantOK   bool
		wantNext time.Duration
	}
	tests := []struct {
		name  string
		plays []play
	}{
		{"first play is allowed", []play{{0, g1, u1, true, 0}}},
		{"same user waits out their cooldown in any guild", []play{
			{0, g1, u1, true, 0},
			{15 * time.Second, g2, u1, false, playUserCooldown},
			{playUserCooldown, g2, u1, true, 0},
		}},
		{"another user in the same guild waits out the guild", []play{
			{0, g1, u1, true, 0},
			{time.Second, g1, u2, false, playGuildCooldown},
			{playGuildCooldown, g1, u2, true, 0},
		}},
		{"other guild and user are independent", []play{
			{0, g1, u1, true, 0},
			{0, g2, u2, true, 0},
		}},
		{"refusal reports the later of the two", []play{
			{0, g1, u1, true, 0},
			{5 * time.Second, g2, u2, true, 0},
			{6 * time.Second, g2, u1, false, playUserCooldown},
		}},
		{"a refusal does not extend the cooldown", []play{
			{0, g1, u1, true, 0},
			{29 * time.Second, g1, u1, false, playUserCooldown},
			{playUserCooldown, g1, u1, true, 0},
		}},
	}
	start := time.Unix(1_000_000, 0)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var now time.Time
			c := cooldown{now: func() time.Time { return now }}
			for i, p := range tt.plays {
				now = start.Add(p.at)
				next, ok := c.take(p.guild, p.user)
				if ok != p.wantOK {
					t.Fatalf("play %d: ok = %v, want %v", i, ok, p.wantOK)
				}
				if !ok && !next.Equal(start.Add(p.wantNext)) {
					t.Errorf("play %d: next = +%s, want +%s", i, next.Sub(start), p.wantNext)
				}
			}
		})
	}
}

// The maps must not keep an entry per user who has ever played.
func TestCooldownPrunesExpired(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	c := cooldown{now: func() time.Time { return now }}
	for i := range 100 {
		c.take(snowflake.ID(i), snowflake.ID(i))
	}
	now = now.Add(playUserCooldown)
	c.take(1000, 1000)
	if len(c.users) != 1 || len(c.guilds) != 1 {
		t.Errorf("left %d users and %d guilds, want 1 each", len(c.users), len(c.guilds))
	}
}

func TestCooldownLetsOneOfARaceThrough(t *testing.T) {
	var c cooldown
	var allowed atomic.Int32
	var wg sync.WaitGroup
	for range 50 {
		wg.Go(func() {
			if _, ok := c.take(1, 1); ok {
				allowed.Add(1)
			}
		})
	}
	wg.Wait()
	if n := allowed.Load(); n != 1 {
		t.Errorf("%d plays allowed, want 1", n)
	}
}
