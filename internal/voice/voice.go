package voice

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/voice"
	"github.com/disgoorg/snowflake/v2"
)

const (
	joinTimeout = 10 * time.Second
	// Healthy handshakes settled in under 1.5s in production; a stalled one never settles, so a
	// longer wait only delays the rejoin that does get through.
	daveTimeout = 3 * time.Second
	// ClipTimeout bounds one clip; a visit playing several gets one each.
	ClipTimeout = 30 * time.Second
	// joinChime is how long Discord's join sound covers the start of a clip played straight after
	// joining. Suspense counts towards it, so only a shorter one waits the difference.
	joinChime = time.Second
)

// The stage a play reached. Returned to the caller, which stores it as "<stage>_fail".
const (
	StageJoin     = "join"
	StageSuspense = "suspense"
	StagePlay     = "play"
)

var (
	ErrBusy         = errors.New("already connected in this guild")
	ErrEveryoneLeft = errors.New("everyone left")
	// ErrPlayTimeout is a clip that outran ClipTimeout. A sentinel rather than an inline error so
	// the caller can tell a truncation apart from a stage that actually broke: the audio played, it
	// just did not finish, and the fix is to re-encode the file rather than to look at the bot.
	ErrPlayTimeout = errors.New("play timeout")
	errDaveStalled = errors.New("dave handshake timeout")
)

// Clip is one file a visit plays and the silence before it, which the first clip's suspense covers.
type Clip struct {
	File string
	Gap  time.Duration
}

type Opts struct {
	Suspense time.Duration // silence between joining and playing
	// StillPopulated is polled after suspense and before every later clip; return false to stop
	// instead of playing to an empty room.
	StillPopulated func() bool
	// Silent leaves after suspense without playing: the fake-out.
	Silent bool
	// Leaving runs on every return once a join was attempted, while the bot is still in the channel.
	Leaving func()
}

// busy tracks guilds we're currently in (value: cancel func), so two ticks / a command can't
// double-join, and /leave can cut a play short.
var busy sync.Map // guildID → context.CancelFunc

func Busy(guild snowflake.ID) bool { _, ok := busy.Load(guild); return ok }

// Cancel aborts an in-progress play in the guild, if any. The deferred Close still runs.
func Cancel(guild snowflake.ID) bool {
	if v, ok := busy.Load(guild); ok {
		if cancel, ok := v.(context.CancelFunc); ok {
			cancel()
			return true
		}
	}
	return false
}

// Shutdown cuts every play short, as /leave does, and waits for each to leave. busy.Delete runs
// after a play's own conn.Close, so an empty map means no conn is still mid-teardown when the
// client's VoiceManager.Close would force it.
func Shutdown(ctx context.Context) {
	busy.Range(func(_, v any) bool {
		if cancel, ok := v.(context.CancelFunc); ok {
			cancel()
		}
		return true
	})
	t := time.NewTicker(50 * time.Millisecond)
	defer t.Stop()
	for {
		empty := true
		busy.Range(func(_, _ any) bool { empty = false; return false })
		if empty {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Play joins, optionally sits in silence, plays each clip in turn on the one connection, leaves.
// Always leaves. Returns the stage it failed in ("join", "suspense", "play") for the stats table.
func Play(ctx context.Context, client *bot.Client, guild, channel snowflake.ID, clips []Clip, o Opts) (stage string, err error) { //nolint:gocyclo // one error branch per join/dave/suspense/play stage; splitting hides the sequence
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if _, loaded := busy.LoadOrStore(guild, cancel); loaded {
		return StageJoin, ErrBusy
	}
	defer busy.Delete(guild)

	stage = StageJoin
	conn, err := join(ctx, client, guild, channel)
	defer leave(conn)
	if o.Leaving != nil {
		defer o.Leaving()
	}
	if err != nil {
		return stage, err
	}

	if o.Suspense > 0 {
		stage = StageSuspense
		if err := sleep(ctx, o.Suspense); err != nil {
			return stage, err
		}
		if o.StillPopulated != nil && !o.StillPopulated() {
			return stage, ErrEveryoneLeft
		}
	}
	if o.Silent {
		return stage, nil
	}

	stage = StagePlay
	if err = sleep(ctx, joinChime-o.Suspense); err != nil {
		return stage, err
	}
	if err = conn.SetSpeaking(ctx, voice.SpeakingFlagMicrophone); err != nil {
		return stage, err
	}
	if err = playClips(ctx, conn, clips, o.StillPopulated); err != nil {
		return stage, err
	}
	// let the last frame drain before tearing the UDP socket down
	time.Sleep(100 * time.Millisecond)
	return stage, nil
}

func playClips(ctx context.Context, conn voice.Conn, clips []Clip, stillPopulated func() bool) error {
	for i, c := range clips {
		if i > 0 {
			if err := sleep(ctx, c.Gap); err != nil {
				return err
			}
			// The first clip played, so an emptied room ends the visit early rather than failing it.
			if stillPopulated != nil && !stillPopulated() {
				return nil
			}
		}
		if err := playClip(ctx, conn, c.File); err != nil {
			return err
		}
	}
	return nil
}

func sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	select {
	case <-time.After(d):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// playClip returns once the file has been handed out. The provider stays set and plays silence
// until the next clip replaces it, which is what holds a gap open without leaving.
func playClip(ctx context.Context, conn voice.Conn, file string) error {
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }() //nolint:errcheck // read-only playback source

	done := make(chan struct{})
	conn.SetOpusFrameProvider(&notifyingProvider{inner: newOggOpusReader(f), done: done})
	select {
	case <-done:
		return nil
	case <-time.After(ClipTimeout):
		return ErrPlayTimeout
	case <-ctx.Done():
		return ctx.Err()
	}
}

// join connects and waits out the DAVE handshake, rejoining once if it stalls: Discord sometimes
// never answers the bot's key package, and a fresh connection gets through where waiting does not.
// The returned conn is never nil and is the caller's to leave, error or not.
func join(ctx context.Context, client *bot.Client, guild, channel snowflake.ID) (voice.Conn, error) {
	for attempt := 1; ; attempt++ {
		conn := client.VoiceManager.CreateConn(guild)
		err := open(ctx, conn, channel)
		if attempt == 2 || !errors.Is(err, errDaveStalled) || ctx.Err() != nil {
			return conn, err
		}
		slog.Warn("rejoining after a stalled dave handshake", slog.Any("guild", guild), slog.Any("err", err))
		leave(conn)
	}
}

func open(ctx context.Context, conn voice.Conn, channel snowflake.ID) error {
	jctx, cancel := context.WithTimeout(ctx, joinTimeout)
	defer cancel()
	if err := conn.Open(jctx, channel, false, true); err != nil { // selfMute=false, selfDeaf=true
		return err
	}
	// DAVE (E2EE) has to finish its MLS handshake before frames are accepted.
	dctx, dcancel := context.WithTimeout(ctx, daveTimeout)
	defer dcancel()
	return waitDave(dctx, conn)
}

func leave(conn voice.Conn) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn.Close(ctx) // also drops the conn from the manager
	// After conn.Close, not before: closing the session first could cut off an MLS message the
	// conn is still sending as it tears down.
	closeSession(conn)
}

func waitDave(ctx context.Context, conn any) error {
	t := time.NewTicker(50 * time.Millisecond)
	defer t.Stop()
	for !daveReady(conn) {
		select {
		case <-ctx.Done():
			return fmt.Errorf("%w (%s)", errDaveStalled, daveState(conn))
		case <-t.C:
		}
	}
	return nil
}

// notifyingProvider closes done at io.EOF and plays silence after it: an empty frame makes disgo
// v0.19.6 call SetSpeaking(None), which deadlocks against conn.Close and wedges gateway dispatch.
type notifyingProvider struct {
	inner voice.OpusFrameProvider
	done  chan struct{}
	once  sync.Once
}

func (p *notifyingProvider) ProvideOpusFrame() ([]byte, error) {
	b, err := p.inner.ProvideOpusFrame()
	if errors.Is(err, io.EOF) {
		p.once.Do(func() { close(p.done) })
		return voice.SilenceAudioFrame, nil
	}
	return b, err
}

func (p *notifyingProvider) Close() { p.inner.Close() }
