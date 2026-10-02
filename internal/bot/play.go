package bot

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/snowflake/v2"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/be-sandaa/coucou/internal/bus"
	"github.com/be-sandaa/coucou/internal/events"
	"github.com/be-sandaa/coucou/internal/voice"
)

// The outcomes a finished play is recorded under. They reach the plays table and the metric label,
// so they are schema: renaming one is a migration, not a wording change.
const (
	outcomeEmpty   = "empty"
	outcomeTimeout = "play_timeout"
	outcomeFakeOut = "fakeout"
)

// reason classifies a failed play into the outcome stored against it, which is also the metric
// label. The bool is false when nothing actually happened and there is nothing to report.
//
// A timeout gets its own outcome rather than "<stage>_fail": nothing broke, the clip is simply
// longer than the bot will sit through, and the fix is to re-encode the file. Lumping it in with
// real failures is what made an over-long sound read as the bot being broken.
func reason(err error, stage string) (string, bool) {
	switch {
	case errors.Is(err, voice.ErrBusy):
		return "", false
	case errors.Is(err, voice.ErrEveryoneLeft):
		return outcomeEmpty, true
	case errors.Is(err, voice.ErrPlayTimeout):
		return outcomeTimeout, true
	default:
		return stage + "_fail", true
	}
}

// fleeable are the triggers where leaving mid-play counts against a listener. Walking out of a
// /play you ran yourself is not fleeing.
var fleeable = map[events.Trigger]bool{events.TriggerLoop: true, events.TriggerEncore: true}

// fled is who was in the room when the bot arrived and gone by the time it left.
func fled(before, after []snowflake.ID) []snowflake.ID {
	stayed := make(map[snowflake.ID]bool, len(after))
	for _, u := range after {
		stayed[u] = true
	}
	var out []snowflake.ID
	for _, u := range before {
		if !stayed[u] {
			out = append(out, u)
		}
	}
	return out
}

// PlayRequest is one request to play something. It is not a bus event: the loop and /play call the
// player directly, so there is no delivery between asking and playing, and nothing to marshal.
//
// Sound may be empty, which means "pick one at play time" — the player is the last moment before
// the clip is read, so a file added since the request was made is fair game.
type PlayRequest struct {
	Guild    snowflake.ID
	Channel  snowflake.ID
	Sound    string
	Trigger  string
	User     *snowflake.ID
	Suspense time.Duration
	FakeOut  bool
	Encore   bool
}

// play is one join→play→leave, reporting the outcome on the bus whatever happened — except when
// the guild was already busy, where nothing happened and there is nothing to report. The error is
// the visit's own, for deciding what follows it; the outcome has already been published.
func play(ctx context.Context, c *bot.Client, b *bus.Bus, e *PlayRequest, sound, file string) error {
	humans := voice.Humans(c, e.Guild, e.Channel)
	started := time.Now()

	var stayed []snowflake.ID
	stage, err := voice.Play(ctx, c, e.Guild, e.Channel, file, voice.Opts{
		Suspense:       e.Suspense,
		Silent:         e.FakeOut,
		StillPopulated: func() bool { return len(voice.Humans(c, e.Guild, e.Channel)) > 0 },
		Leaving:        func() { stayed = voice.Humans(c, e.Guild, e.Channel) },
	})
	out := bus.PlayFinished{
		Guild: e.Guild, Channel: e.Channel, Sound: sound, Trigger: e.Trigger, User: e.User,
		Listeners: humans, OK: err == nil && !e.FakeOut, StartedAt: started, Duration: time.Since(started),
	}
	if err == nil && e.FakeOut {
		out.Reason = outcomeFakeOut
	}
	if fleeable[events.Trigger(e.Trigger)] {
		out.Fled = fled(humans, stayed)
	}
	if err != nil {
		r, report := reason(err, stage)
		if !report {
			return err // nothing happened, so there is nothing to publish
		}
		out.Reason = r
		span := trace.SpanFromContext(ctx)
		span.SetAttributes(attribute.String("coucou.stage", stage))
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		slog.Warn("play failed", slog.Any("guild", e.Guild), slog.Any("channel", e.Channel),
			slog.String("sound", sound), slog.String("stage", stage), slog.Any("err", err))
	}
	b.Publish(ctx, out)
	return err
}

// encoreDue is whether a visit comes back: only a loop visit that went through, a clean fake-out
// included. An encore is not a loop visit, so it never chains.
func encoreDue(e *PlayRequest, err error) bool {
	return e.Encore && err == nil && e.Trigger == string(events.TriggerLoop)
}

// encore waits out the gap, then asks the player again. It runs outside the pool so the wait holds
// no slot. ctx is the loop's, which run cancels before any stop, so a pending encore is gone before
// Bus.Close drains — and the player refuses a cancelled ctx, the same guard the loop relies on.
//
// welcome repeats the loop's checks, since the room had up to thirty seconds to change. A no drops
// the encore without a record: nothing was attempted.
func encore(ctx context.Context, wait time.Duration, next *PlayRequest, welcome func(*PlayRequest) bool, player func(context.Context, *PlayRequest) error) {
	t := time.NewTimer(wait)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return
	case <-t.C:
	}
	if !welcome(next) {
		return
	}
	if err := player(ctx, next); err != nil {
		slog.Debug("encore: refused", slog.Any("err", err))
	}
}
