package bot

import (
	"iter"
	"sync/atomic"
	"time"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/events"
)

// GatewayLatency reports the heartbeat round trip to alert on: the worst shard's, or zero as soon
// as any shard is not answering. Read at scrape time, so it touches nothing but the shards.
//
// The bot runs under a shard manager, so Client.Gateway is nil and the shards are the only place
// a latency exists. A guild count of 1,600 earns one shard from Discord, which makes "worst" the
// same as "the" today — it is stated as worst so that stays true if the count ever grows.
func GatewayLatency(c *bot.Client) func() time.Duration {
	return func() time.Duration {
		if c.ShardManager == nil {
			return 0
		}
		return worstLatency(func(yield func(time.Duration) bool) {
			for g := range c.ShardManager.Shards() {
				if !yield(g.Latency()) {
					return
				}
			}
		})
	}
}

// worstLatency folds per-shard latencies into the single number the gauge exports. Anything not
// positive is a shard with no answer — disgo computes received-minus-sent, so a heartbeat in
// flight reads negative — and one silent shard makes the whole bot's gateway health zero rather
// than hiding behind a healthy sibling.
func worstLatency(latencies iter.Seq[time.Duration]) time.Duration {
	var worst time.Duration
	for l := range latencies {
		if l <= 0 {
			return 0
		}
		worst = max(worst, l)
	}
	return worst // no shards yet: not connected, which reads as zero for the same reason
}

// Pulse records the last dispatched heartbeat ACK. ACKs pass the event lock all shards share, so a
// wedged listener stops the pulse, while a lone zombie shard reconnects and resumes it.
type Pulse struct{ last atomic.Int64 }

// NewPulse starts the clock now, so the bot gets one full window to connect before it is stale.
func NewPulse() *Pulse {
	p := &Pulse{}
	p.last.Store(time.Now().UnixNano())
	return p
}

func (p *Pulse) OnHeartbeatAck() bot.EventListener {
	return bot.NewListenerFunc(func(*events.HeartbeatAck) { p.last.Store(time.Now().UnixNano()) })
}

// Age is the time since the last ACK, or since NewPulse if none has arrived.
func (p *Pulse) Age() time.Duration { return time.Since(time.Unix(0, p.last.Load())) }
