package bus

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/ThreeDotsLabs/watermill/message/router/middleware"
	"github.com/ThreeDotsLabs/watermill/pubsub/gochannel"
	"github.com/disgoorg/snowflake/v2"
)

// newTestBus builds a bus and runs its router, returning once every subscriber is
// attached. Nothing may be published before that point — see TestPublishBeforeRunningIsNotDelivered.
const (
	nameFirst  = "first"
	nameSecond = "second"
	nameInTime = "in-time"
	valCarried = "carried"
	trigLoop   = "loop"
	soundBoom  = "boom"
	sndAirhorn = "airhorn"
	rsnJoinIt  = "join_fail"
	trigCmd    = "command"
)

func newTestBus(t *testing.T, wire func(b *Bus)) *Bus {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	b, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	wire(b)

	done := make(chan error, 1)
	go func() { done <- b.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(15 * time.Second):
			t.Error("router did not stop")
		}
	})

	select {
	case <-b.Running():
	case <-time.After(10 * time.Second):
		t.Fatal("router never started")
	}
	return b
}

func TestTypedHandlerReceivesEvent(t *testing.T) {
	got := make(chan PlayFinished, 1)
	b := newTestBus(t, func(b *Bus) {
		On(b, "test-play", func(_ context.Context, e *PlayFinished) error {
			got <- *e
			return nil
		})
	})

	want := PlayFinished{
		Guild: snowflake.ID(123), Channel: snowflake.ID(456),
		Sound: soundBoom, Trigger: trigLoop, Duration: 3 * time.Second,
		StartedAt: time.Now().UTC().Truncate(time.Second),
	}
	b.Publish(context.Background(), want)

	select {
	case e := <-got:
		if e.Guild != want.Guild || e.Channel != want.Channel || e.Sound != want.Sound ||
			e.Trigger != want.Trigger || e.Duration != want.Duration {
			t.Errorf("got %+v, want %+v", e, want)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("handler never received the event")
	}
}

func TestRecoveredHandlerDoesNotStopOthers(t *testing.T) {
	survivor := make(chan string, 4)
	// Signalled by the faulting handler rather than counted and read at the end. It runs on its
	// own subscriber goroutine, and nothing orders that against the survivor's delivery, so a
	// counter read after the survivor's second message was a coin toss.
	//
	// The send is non-blocking: the retry middleware runs this handler several times per message,
	// and a full channel would stall the retry rather than the test.
	faulted := make(chan struct{}, 1)

	b := newTestBus(t, func(b *Bus) {
		// Two handlers on one event: the first always faults, the second must still run, and the
		// router must stay up for the next event. Recoverer middleware is what makes that true.
		On(b, "test-faulting", func(_ context.Context, e *SoundAdded) error {
			select {
			case faulted <- struct{}{}:
			default:
			}
			panic("deliberate test fault for " + e.Name)
		})
		On(b, "test-survivor", func(_ context.Context, e *SoundAdded) error {
			survivor <- e.Name
			return nil
		})
	})

	b.Publish(context.Background(), SoundAdded{Name: nameFirst})
	select {
	case name := <-survivor:
		if name != nameFirst {
			t.Errorf("got %q, want %q", name, nameFirst)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the surviving handler never ran")
	}

	b.Publish(context.Background(), SoundAdded{Name: nameSecond})
	select {
	case name := <-survivor:
		if name != nameSecond {
			t.Errorf("got %q, want %q", name, nameSecond)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the bus stopped delivering after a recovered fault")
	}

	select {
	case <-faulted:
	case <-time.After(10 * time.Second):
		t.Error("the faulting handler never ran, so this proved nothing")
	}
}

// The bus is gochannel with Persistent off: a subscriber that is not attached yet never
// sees the message. That is why main waits on Running() before it publishes anything.
func TestPublishBeforeRunningIsNotDelivered(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	b, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	got := make(chan string, 2)
	On(b, "test-early", func(_ context.Context, e *SoundAdded) error {
		got <- e.Name
		return nil
	})

	b.Publish(ctx, SoundAdded{Name: "too-early"}) // before Run: nobody is subscribed yet

	done := make(chan error, 1)
	go func() { done <- b.Run(ctx) }()
	defer func() {
		cancel()
		<-done
	}()
	<-b.Running()

	b.Publish(ctx, SoundAdded{Name: nameInTime})

	select {
	case name := <-got:
		if name != nameInTime {
			t.Fatalf("got %q; an event published before Running() must not arrive", name)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the post-Running event never arrived")
	}

	select {
	case name := <-got:
		t.Fatalf("unexpected second delivery %q — the early event should never arrive", name)
	case <-time.After(500 * time.Millisecond):
	}
}

// TestBoundedCapsConcurrency: n slots means at most n bodies run at once, and the handler must not
// have waited for any of them.
func TestBoundedCapsConcurrency(t *testing.T) {
	const slots = 2

	var mu sync.Mutex
	var inFlight, peak int
	release := make(chan struct{})
	done := make(chan struct{}, slots+1)

	h := Bounded(&Bus{}, slots, func(_ context.Context, _ *PlayFinished) {
		mu.Lock()
		inFlight++
		peak = max(peak, inFlight)
		mu.Unlock()
		<-release
		mu.Lock()
		inFlight--
		mu.Unlock()
		done <- struct{}{}
	})

	// Only the first `slots` deliveries can be accepted while the bodies are parked; each returns
	// without blocking, which is the whole point of detaching.
	for range slots {
		if err := h(context.Background(), &PlayFinished{}); err != nil {
			t.Fatalf("handler returned %v, want nil", err)
		}
	}
	// The next one has no slot, so it must block until a body finishes rather than pile on.
	accepted := make(chan error, 1)
	go func() { accepted <- h(context.Background(), &PlayFinished{}) }()
	select {
	case err := <-accepted:
		t.Fatalf("handler accepted a %d-th delivery with %d slots (err %v)", slots+1, slots, err)
	case <-time.After(50 * time.Millisecond):
	}

	close(release)
	if err := <-accepted; err != nil {
		t.Fatalf("handler returned %v once a slot freed, want nil", err)
	}
	for range slots + 1 {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("a detached body never ran")
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if peak > slots {
		t.Errorf("peak concurrency %d, want at most %d", peak, slots)
	}
}

// The trap this wrapper exists to avoid: watermill cancels a message context the moment the handler
// returns, so a body that inherited it would die instantly. It must see the values and not the
// cancellation.
func TestBoundedBodySurvivesTheDelivery(t *testing.T) {
	type key struct{}
	seen := make(chan any, 1)
	alive := make(chan error, 1)

	h := Bounded(&Bus{}, 1, func(ctx context.Context, _ *PlayFinished) {
		seen <- ctx.Value(key{})
		select {
		case <-ctx.Done():
			alive <- ctx.Err()
		case <-time.After(100 * time.Millisecond):
			alive <- nil
		}
	})

	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), key{}, valCarried))
	if err := h(ctx, &PlayFinished{}); err != nil {
		t.Fatalf("handler returned %v, want nil", err)
	}
	cancel() // what watermill does as soon as the handler returns

	if got := <-seen; got != valCarried {
		t.Errorf("ctx value = %v, want the parent's %q", got, valCarried)
	}
	if err := <-alive; err != nil {
		t.Errorf("body was cancelled with its delivery (%v); it must outlive it", err)
	}
}

// A cancelled context is the bus shutting down: the delivery is refused, not queued.
func TestBoundedRefusesWhenCancelled(t *testing.T) {
	h := Bounded(&Bus{}, 1, func(context.Context, *PlayFinished) {
		t.Error("body ran for a cancelled delivery")
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := h(ctx, &PlayFinished{}); err == nil {
		t.Fatal("handler returned nil for a cancelled delivery, want the context error")
	}
}

// The bug this drain exists for: a play detached by Bounded outlives its delivery, so without the
// wait the process could exit mid-clip and skip voice.Play's deferred conn.Close, leaving the bot
// sitting in the channel until Discord timed the session out.
func TestCloseWaitsForDetachedBodies(t *testing.T) {
	b := &Bus{}
	running, finish := make(chan struct{}), make(chan struct{})

	h := Bounded(b, 1, func(context.Context, *PlayFinished) {
		close(running)
		<-finish
	})
	if err := h(context.Background(), &PlayFinished{}); err != nil {
		t.Fatalf("handler returned %v, want nil", err)
	}
	<-running

	// A budget that expires while the body is parked: the drain reports the overrun, never a
	// finished drain, because reporting one would be the shutdown racing the play.
	short, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if b.waitDetached(short) {
		t.Error("drain reported done while a detached body was still running")
	}

	close(finish)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if !b.waitDetached(ctx) {
		t.Error("drain timed out after the detached body had finished")
	}
}

// A handler that never succeeds must stop being called once retries run out, and its message must
// land on the poison topic rather than being redelivered for as long as the process lives.
func TestExhaustedMessageIsParked(t *testing.T) {
	prev := retryBudget
	retryBudget = 300 * time.Millisecond
	t.Cleanup(func() { retryBudget = prev })

	var mu sync.Mutex
	calls := 0
	b := newTestBus(t, func(b *Bus) {
		On(b, "test-always-fails", func(context.Context, *SoundAdded) error {
			mu.Lock()
			calls++
			mu.Unlock()
			return errors.New("deliberate test failure")
		})
	})
	ch, ok := b.pub.(tracedPublisher).Publisher.(*gochannel.GoChannel)
	if !ok {
		t.Fatalf("bus publisher is %T, want *gochannel.GoChannel", b.pub)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	parked, err := ch.Subscribe(ctx, poisonTopic)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	b.Publish(context.Background(), SoundAdded{Name: nameFirst})
	select {
	case m := <-parked:
		m.Ack()
		if h := m.Metadata.Get(middleware.PoisonedHandlerKey); h != "test-always-fails" {
			t.Errorf("parked by handler %q, want test-always-fails", h)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the message never reached the poison topic")
	}

	mu.Lock()
	settled := calls
	mu.Unlock()
	time.Sleep(time.Second) // several retry budgets: a redelivery would show up as more calls
	mu.Lock()
	defer mu.Unlock()
	if calls != settled {
		t.Errorf("handler called %d more times after the message was parked", calls-settled)
	}
}
