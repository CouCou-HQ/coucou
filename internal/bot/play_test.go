package bot

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/disgoorg/snowflake/v2"

	"github.com/be-sandaa/coucou/internal/events"
	"github.com/be-sandaa/coucou/internal/voice"
)

// A clip that outran the play timeout is a truncation, not a broken stage: it gets its own outcome
// so an over-long file stops reading as the bot being broken, and so the log can name which file.
func TestReason(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		stage  string
		want   string
		report bool
	}{
		{"busy is not reported at all", voice.ErrBusy, voice.StageJoin, "", false},
		{"everyone left", voice.ErrEveryoneLeft, voice.StagePlay, outcomeEmpty, true},
		{"a timeout is its own outcome", voice.ErrPlayTimeout, voice.StagePlay, outcomeTimeout, true},
		{"anything else is named for its stage", errors.New("boom"), voice.StageJoin, "join_fail", true},
		{"a wrapped sentinel still classifies", fmt.Errorf("dialling: %w", voice.ErrPlayTimeout), voice.StagePlay, outcomeTimeout, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, report := reason(tt.err, tt.stage)
			if got != tt.want || report != tt.report {
				t.Errorf("reason(%v, %q) = %q, %v; want %q, %v", tt.err, tt.stage, got, report, tt.want, tt.report)
			}
		})
	}
}

func TestFled(t *testing.T) {
	const a, b, c = snowflake.ID(1), snowflake.ID(2), snowflake.ID(3)
	tests := []struct {
		name          string
		before, after []snowflake.ID
		want          []snowflake.ID
	}{
		{"nobody left", []snowflake.ID{a, b}, []snowflake.ID{a, b}, nil},
		{"one left", []snowflake.ID{a, b}, []snowflake.ID{a}, []snowflake.ID{b}},
		{"the room emptied", []snowflake.ID{a, b}, nil, []snowflake.ID{a, b}},
		{"alone and left", []snowflake.ID{a}, nil, []snowflake.ID{a}},
		{"a latecomer does not cover for who left", []snowflake.ID{a}, []snowflake.ID{c}, []snowflake.ID{a}},
		{"nobody was there", nil, []snowflake.ID{c}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := fled(tt.before, tt.after); !slices.Equal(got, tt.want) {
				t.Errorf("fled(%v, %v) = %v, want %v", tt.before, tt.after, got, tt.want)
			}
		})
	}
}

func TestEncoreDue(t *testing.T) {
	loop, cmd, enc := string(events.TriggerLoop), string(events.TriggerCommand), string(events.TriggerEncore)
	tests := []struct {
		name    string
		trigger string
		rolled  bool
		err     error
		want    bool
	}{
		{"a loop visit that played", loop, true, nil, true},
		{"not rolled", loop, false, nil, false},
		{"nobody stayed to hear it", loop, true, voice.ErrEveryoneLeft, false},
		{"the guild was busy", loop, true, voice.ErrBusy, false},
		{"the play failed", loop, true, errors.New("boom"), false},
		{"the clip timed out", loop, true, voice.ErrPlayTimeout, false},
		{"/play never comes back", cmd, true, nil, false},
		{"an encore never chains", enc, true, nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := encoreDue(&PlayRequest{Trigger: tt.trigger, Encore: tt.rolled}, tt.err); got != tt.want {
				t.Errorf("encoreDue = %v, want %v", got, tt.want)
			}
		})
	}
}

// Walking out of a /play you ran yourself is not fleeing.
func TestFleeable(t *testing.T) {
	tests := []struct {
		trigger events.Trigger
		want    bool
	}{
		{events.TriggerLoop, true},
		{events.TriggerEncore, true},
		{events.TriggerCommand, false},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(string(tt.trigger), func(t *testing.T) {
			if got := fleeable[tt.trigger]; got != tt.want {
				t.Errorf("fleeable[%q] = %v, want %v", tt.trigger, got, tt.want)
			}
		})
	}
}

// A clean fake-out returns no error from play, so it is covered by "a loop visit that played" above;
// this is the part play() cannot show: the wait, the second look at the room, and shutdown.
func TestEncore(t *testing.T) {
	next := &PlayRequest{Guild: 1, Channel: 2, Sound: "b", Trigger: string(events.TriggerEncore)}
	const alice, bob = snowflake.ID(10), snowflake.ID(11) // bob has opted out
	tests := []struct {
		name        string
		wait        time.Duration
		cancel      bool
		quiet, busy bool
		humans      []snowflake.ID
		refuse      error
		wantAsked   bool
		wantPlay    bool
	}{
		{"welcomed back", time.Millisecond, false, false, false, []snowflake.ID{alice, bob}, nil, true, true},
		{"quiet hours began", time.Millisecond, false, true, false, []snowflake.ID{alice}, nil, true, false},
		{"the guild is busy", time.Millisecond, false, false, true, []snowflake.ID{alice}, nil, true, false},
		{"nobody is left", time.Millisecond, false, false, false, nil, nil, true, false},
		{"only opted-out people remain", time.Millisecond, false, false, false, []snowflake.ID{bob}, nil, true, false},
		{"a refusing pool is not fatal", time.Millisecond, false, false, false, []snowflake.ID{alice}, errors.New("shutting down"), true, true},
		{"shutdown ends the wait", time.Hour, true, false, false, []snowflake.ID{alice}, nil, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var asked, played bool
			room := welcomer(
				func(snowflake.ID) bool { return tt.quiet },
				func(snowflake.ID) bool { return tt.busy },
				func(snowflake.ID, snowflake.ID) []snowflake.ID { return tt.humans },
				func(u snowflake.ID) bool { return u == bob },
			)
			welcome := func(e *PlayRequest) bool { asked = true; return room(e) }
			player := func(got context.Context, e *PlayRequest) error {
				played = true
				if got != ctx || e != next {
					t.Errorf("player got (%v, %+v), want the caller's ctx and the encore request", got, e)
				}
				return tt.refuse
			}
			done := make(chan struct{})
			go func() { defer close(done); encore(ctx, tt.wait, next, welcome, player) }()
			if tt.cancel {
				cancel()
			}
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("encore did not return")
			}
			if asked != tt.wantAsked || played != tt.wantPlay {
				t.Errorf("asked %v, played %v; want %v, %v", asked, played, tt.wantAsked, tt.wantPlay)
			}
		})
	}
}
