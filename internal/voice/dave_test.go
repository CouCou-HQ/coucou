package voice

import (
	"context"
	"errors"
	"testing"

	"github.com/disgoorg/godave"
)

// fakeSession embeds the interface so only the method under test needs a body; anything else
// godave.Session grows is a nil-pointer panic rather than a compile error, which is the trade for
// not hand-writing twenty methods that would never be called.
type fakeSession struct {
	godave.Session
	closed int
	ready  bool
}

func (f *fakeSession) Close() error { f.closed++; return nil }
func (f *fakeSession) Ready() bool  { return f.ready }

func TestCloseSessionClosesItAndForgetsIt(t *testing.T) {
	conn := new(int) // stands in for the conn; the map only needs a comparable key
	f := &fakeSession{}
	sessions.Store(conn, f)

	closeSession(conn)
	if f.closed != 1 {
		t.Errorf("Close called %d times, want 1", f.closed)
	}
	if _, ok := sessions.Load(conn); ok {
		t.Error("the conn is still registered after its session was closed")
	}
}

// Play's teardown runs on every exit path, including ones where disgo used its noop session and
// nothing was ever registered. That has to be silent rather than a panic.
func TestCloseSessionOnAnUnregisteredConn(t *testing.T) {
	other := new(int)
	f := &fakeSession{}
	sessions.Store(other, f)
	t.Cleanup(func() { sessions.Delete(other) })

	closeSession(new(int))

	if f.closed != 0 {
		t.Errorf("closing an unregistered conn closed another conn's session %d times", f.closed)
	}
	if _, ok := sessions.Load(other); !ok {
		t.Error("closing an unregistered conn dropped another conn's registration")
	}
}

// daveReady treats an unregistered conn as ready, because the noop session never encrypts and
// waiting on it would hang every play by the full join timeout.
func TestDaveReady(t *testing.T) {
	if !daveReady(new(int)) {
		t.Error("an unregistered conn is not ready, so every play would wait out the join timeout")
	}

	conn := new(int)
	f := &fakeSession{}
	sessions.Store(conn, f)
	t.Cleanup(func() { sessions.Delete(conn) })

	if daveReady(conn) {
		t.Error("a session mid-handshake reported ready, so frames would go out unencrypted")
	}
	f.ready = true
	if !daveReady(conn) {
		t.Error("a session with an active epoch is not reported ready")
	}
}

// holdingSession is dave-go's shape: it can say the channel will never have E2EE, which Ready cannot.
type holdingSession struct {
	fakeSession
	hold bool
}

func (h *holdingSession) ShouldHoldFrames() bool { return h.hold }

func TestDaveReadyOnATransportOnlyChannel(t *testing.T) {
	conn := new(int)
	h := &holdingSession{hold: false} // protocol version 0: Ready is false and stays false
	sessions.Store(conn, h)
	t.Cleanup(func() { sessions.Delete(conn) })

	if !daveReady(conn) {
		t.Error("a v0 channel is not ready, so every play there waits out the join timeout")
	}
	h.hold = true
	if daveReady(conn) {
		t.Error("a session mid-handshake reported ready, so frames would go out unencrypted")
	}
}

// join rejoins only on errDaveStalled, so a stall that surfaced as any other error would never retry.
func TestWaitDaveStallIsRetryable(t *testing.T) {
	conn := new(int)
	sessions.Store(conn, &fakeSession{})
	t.Cleanup(func() { sessions.Delete(conn) })

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := waitDave(ctx, conn); !errors.Is(err, errDaveStalled) {
		t.Errorf("stalled handshake returned %v, want errDaveStalled", err)
	}
}
