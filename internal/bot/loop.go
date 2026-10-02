package bot

import (
	"context"
	"log/slog"
	"math/rand/v2"
	"time"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/snowflake/v2"

	"github.com/be-sandaa/coucou/internal/chaos"
	"github.com/be-sandaa/coucou/internal/events"
	"github.com/be-sandaa/coucou/internal/optout"
	"github.com/be-sandaa/coucou/internal/settings"
	"github.com/be-sandaa/coucou/internal/sounds"
	"github.com/be-sandaa/coucou/internal/voice"
)

const (
	interval = 5 * time.Minute
	spread   = 4 * time.Minute // joins spread over the first 4 min of each window
)

// Loop is the scheduler behind the bot's random visits: every five minutes it rolls the dice for
// each guild and plays somewhere.
type Loop struct {
	client   *bot.Client
	settings *settings.Store
	sounds   *sounds.Registry
	play     func(context.Context, *PlayRequest) error
	optouts  *optout.Store
	chaos    *chaos.Store
}

// NewLoop takes what the loop reads: who is configured, whether there is anything to play, and the
// player to hand a pick to. The client is only ever used to look up channels in the cache.
func NewLoop(client *bot.Client, set *settings.Store, reg *sounds.Registry, play func(context.Context, *PlayRequest) error, opt *optout.Store, ch *chaos.Store) *Loop {
	return &Loop{client: client, settings: set, sounds: reg, play: play, optouts: opt, chaos: ch}
}

// best is the cache lookup, split out so the test can substitute one and need no Discord at all.
func (l *Loop) best(guild snowflake.ID) (snowflake.ID, []snowflake.ID) {
	// The loop is the only caller that passes the opt-out set: it is the one deciding to drop in
	// on people uninvited, which is the thing they opted out of.
	return voice.Best(l.client, guild, l.optouts.Has)
}

type pick struct {
	guild, channel snowflake.ID
	suspense       time.Duration
	fakeOut        bool
	encore         bool
}

// candidates: dice first (cheapest), then clock, then cache lookups.
//
// best and busy are the two things it needs from outside the settings map, passed in rather than
// read off the Loop so the test can substitute stubs and need no Discord at all. Production passes
// the cache-backed and voice-manager-backed versions.
func (l *Loop) candidates(
	now time.Time,
	best func(snowflake.ID) (snowflake.ID, []snowflake.ID),
	busy func(snowflake.ID) bool,
) []pick {
	var out []pick
	for guild, st := range l.settings.Configured() {
		chance := l.chaos.Chance(guild, st.Chance, now)
		if chance == 0 || rand.IntN(100) >= chance {
			continue
		}
		if l.settings.IsQuiet(st, now) || busy(guild) {
			continue
		}
		ch, humans := best(guild)
		if len(humans) == 0 {
			continue
		}
		var s time.Duration
		var fake bool
		if st.Suspense > 0 {
			// /suspense sets a maximum, so the draw has to fit inside it. Three seconds is the
			// floor worth pausing for, clamped down when the guild asked for less — an unclamped
			// floor of 3 made `/suspense 1` wait three times what it was asked for.
			lo := min(3, st.Suspense)
			s = time.Duration(lo+rand.IntN(st.Suspense-lo+1)) * time.Second
			fake = rand.IntN(100) < st.FakeOut
		}
		out = append(out, pick{guild, ch, s, fake, rand.IntN(100) < st.Encore})
	}
	return out
}

func (l *Loop) Run(ctx context.Context) error {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
		if l.sounds.Len() == 0 {
			continue
		}
		picks := l.candidates(time.Now(), l.best, voice.Busy)
		if len(picks) == 0 {
			continue
		}
		gap := spread / time.Duration(len(picks))
		slog.Info("tick", slog.Int("joins", len(picks)), slog.Duration("gap", gap))
		for _, p := range picks {
			// The loop only decides *what*; the player decides *when it can*. A full pool makes this
			// wait, which is the backpressure — and delaying the next pick is the right thing for a
			// loop whose next tick is five minutes away. Sound is picked at play time so a file added
			// during the 4-minute spread is fair game.
			if err := l.play(ctx, &PlayRequest{Guild: p.guild, Channel: p.channel, Trigger: string(events.TriggerLoop), Suspense: p.suspense, FakeOut: p.fakeOut, Encore: p.encore}); err != nil {
				return err // only ever the pool refusing because we are shutting down
			}
			select {
			case <-time.After(gap):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
}
