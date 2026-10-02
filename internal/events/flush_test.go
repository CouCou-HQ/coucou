package events

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/be-sandaa/coucou/internal/store"
)

// down is a store whose writes fail, after running during — which is where rows that arrive while a
// flush is in flight get recorded.
type down struct {
	capture
	during func()
}

var errDown = errors.New("database down")

func (d *down) WritePlays(context.Context, []store.Play) error { d.during(); return errDown }
func (d *down) WriteMisc(context.Context, []store.Misc) error  { d.during(); return errDown }

func TestFailedFlushKeepsTheBufferCapped(t *testing.T) {
	d := &down{}
	l := New(d)
	_ = withConsole(t, slog.LevelError+1) // the flush failures are the point, not noise to read

	for range maxBuffer {
		l.RecordPlay(Play{})
		l.Record(Misc{At: time.Now(), Kind: kindCommand})
	}
	d.during = func() {
		for range 10 {
			l.RecordPlay(Play{})
			l.Record(Misc{At: time.Now(), Kind: kindCommand})
		}
	}
	for range 3 {
		l.Flush(context.Background())
	}

	if plays, misc := l.Buffered(); plays != maxBuffer || misc != maxBuffer {
		t.Fatalf("Buffered() = %d plays, %d misc; want both capped at %d", plays, misc, maxBuffer)
	}
}
