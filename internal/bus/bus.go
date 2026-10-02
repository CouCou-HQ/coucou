package bus

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill/components/cqrs"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/ThreeDotsLabs/watermill/message/router/middleware"
	"github.com/ThreeDotsLabs/watermill/pubsub/gochannel"
	"golang.org/x/sync/semaphore"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"github.com/be-sandaa/coucou/internal/metrics"
	"github.com/be-sandaa/coucou/internal/tracing"
)

// tracedPublisher writes the current trace context into each message's metadata on the way out.
// It has to be a publisher decorator rather than part of the marshaler: cqrs.EventBus marshals
// first and only calls SetContext afterwards, so the marshaler never sees the publisher's context.
type tracedPublisher struct{ message.Publisher }

func (p tracedPublisher) Publish(topic string, msgs ...*message.Message) error {
	for _, m := range msgs {
		otel.GetTextMapPropagator().Inject(m.Context(), propagation.MapCarrier(m.Metadata))
	}
	return p.Publisher.Publish(topic, msgs...)
}

// traced starts a span per delivery, continuing the trace the publisher wrote into the metadata.
// A consumer link is what makes the publish and the delivery one trace.
func traced(h message.HandlerFunc) message.HandlerFunc {
	return func(msg *message.Message) ([]*message.Message, error) {
		name := message.HandlerNameFromCtx(msg.Context())
		ctx := otel.GetTextMapPropagator().Extract(msg.Context(), propagation.MapCarrier(msg.Metadata))
		ctx, span := tracing.Tracer().Start(ctx, "bus consume "+name,
			trace.WithSpanKind(trace.SpanKindConsumer),
			trace.WithAttributes(
				attribute.String("messaging.consumer.group.name", name),
				attribute.String("messaging.message.id", msg.UUID),
			),
		)
		defer span.End()

		msg.SetContext(ctx)
		out, err := h(msg)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}
		return out, err
	}
}

const poisonTopic = "coucou.poison"

// retryBudget is long enough to ride out a database failover. A var so tests can shorten it.
var retryBudget = 30 * time.Second

func retry(logger watermill.LoggerAdapter) message.HandlerMiddleware {
	return middleware.Retry{
		MaxRetries:      20, // retryBudget is the real bound; this only has to be out of its way
		InitialInterval: 200 * time.Millisecond,
		MaxInterval:     5 * time.Second,
		Multiplier:      2,
		MaxElapsedTime:  retryBudget,
		Logger:          logger,
	}.Middleware
}

// Bus wraps a watermill publisher/subscriber pair, a router, and the CQRS event bus/processor that
// give us typed handlers. Callers see Publish(ctx, event) and On(handler); they never see topics.
type Bus struct {
	pub       message.Publisher
	router    *message.Router
	events    *cqrs.EventBus
	processor *cqrs.EventProcessor
	marshaler cqrs.CommandEventMarshaler
	logger    watermill.LoggerAdapter
	detached  sync.WaitGroup // bodies started by Bounded, which outlive their delivery; Close waits on them
}

// New builds the in-process bus and wires the router middleware: gochannel, at-most-once, zero
// infra — one binary per bot needs nothing running next to it.
func New() (*Bus, error) {
	logger := watermill.NewSlogLogger(slog.Default())

	ps := gochannel.NewGoChannel(gochannel.Config{
		OutputChannelBuffer:            256,
		Persistent:                     false, // late subscribers don't replay; we wire everything before Run
		BlockPublishUntilSubscriberAck: false,
	}, logger)
	var pub message.Publisher = tracedPublisher{ps}

	router, err := message.NewRouter(message.RouterConfig{CloseTimeout: 10 * time.Second}, logger)
	if err != nil {
		return nil, err
	}
	// A nack is redelivered forever, so a message that still fails once retries run out is parked
	// on coucou.poison and acked instead. The log line is the only trace: nothing subscribes there.
	poison, err := middleware.PoisonQueueWithFilter(pub, poisonTopic, func(err error) bool {
		slog.Error("bus: retries exhausted, message parked on "+poisonTopic, slog.Any("err", err))
		return true
	})
	if err != nil {
		return nil, err
	}

	router.AddMiddleware(
		middleware.CorrelationID,
		poison,
		traced, // outside retries, so the span covers them too; inside poison, so it still sees the failure
		observe,
		middleware.Recoverer, // a panicking handler nacks instead of taking the process down
		retry(logger),
		middleware.Timeout(90*time.Second), // a play is ≤ 10 join + 20 suspense + 30 play; nothing legit exceeds this
	)

	// One marshaler for both sides: publishing and consuming must agree on the codec, and it builds
	// a cbor.EncMode that is worth making once.
	marshaler := newMarshaler()
	topic := func(name string) string { return "coucou." + strings.ToLower(name) }

	events, err := cqrs.NewEventBusWithConfig(pub, cqrs.EventBusConfig{
		GeneratePublishTopic: func(p cqrs.GenerateEventPublishTopicParams) (string, error) { return topic(p.EventName), nil },
		Marshaler:            marshaler,
		Logger:               logger,
	})
	if err != nil {
		return nil, err
	}
	processor, err := cqrs.NewEventProcessorWithConfig(router, cqrs.EventProcessorConfig{
		GenerateSubscribeTopic: func(p cqrs.EventProcessorGenerateSubscribeTopicParams) (string, error) {
			return topic(p.EventName), nil
		},
		// gochannel fans out to every subscriber, so every handler can share the one.
		SubscriberConstructor: func(cqrs.EventProcessorSubscriberConstructorParams) (message.Subscriber, error) {
			return ps, nil
		},
		Marshaler: marshaler,
		Logger:    logger,
	})
	if err != nil {
		return nil, err
	}
	return &Bus{pub: pub, router: router, events: events, processor: processor, marshaler: marshaler, logger: logger}, nil
}

// Publish sends one typed event. Fire-and-forget; errors are logged, never propagated to gateway handlers.
func (b *Bus) Publish(ctx context.Context, ev any) {
	if err := b.events.Publish(ctx, ev); err != nil {
		slog.Error("bus: publish", slog.String("event", fmt.Sprintf("%T", ev)), slog.Any("err", err))
	}
}

// observe times every delivery and counts its outcome. It sits outside Retry so one delivery is
// one observation whatever the retries do, and the handler name comes from the router rather than
// from each consumer having to repeat it.
func observe(h message.HandlerFunc) message.HandlerFunc {
	return func(msg *message.Message) ([]*message.Message, error) {
		name := message.HandlerNameFromCtx(msg.Context())
		started := time.Now()
		out, err := h(msg)
		metrics.BusHandleDuration.WithLabelValues(name).Observe(time.Since(started).Seconds())
		outcome := "ok"
		if err != nil {
			outcome = "error"
		}
		metrics.BusHandled.WithLabelValues(name, outcome).Inc()
		return out, err
	}
}

// Handler is the shape a consumer takes: one typed event, and an error that decides ack or nack.
// An alias, not a defined type, so a plain func literal is one without being converted.
type Handler[T any] = func(ctx context.Context, ev *T) error

// On registers a typed handler. Name is the consumer identity: it labels the handler's metrics and
// spans, so keep it stable across releases.
func On[T any](b *Bus, name string, fn Handler[T]) {
	if err := b.processor.AddHandlers(cqrs.NewEventHandler(name, fn)); err != nil {
		panic(fmt.Sprintf("bus: add handler %s: %v", name, err)) // a registration bug, fail at boot
	}
}

// Bounded runs a handler detached from its delivery, at most n at a time. The bus handler returns
// as soon as a slot is free, so the bus is never blocked for the length of the work, and n — not
// the bus — is the concurrency limit. The slot is the backpressure: once n are in flight the next
// delivery waits, and a cancelled ctx (the bus shutting down) is refused rather than queued.
//
// The body keeps the delivery's context values but not its cancellation: watermill cancels a
// message context the moment the handler returns (gochannel Config.preserveContext is off), and
// work that outlives its delivery has to survive that. Nothing else will stop the body, so wrap
// fn in Timeout — a detached handler with no deadline is a goroutine with no termination path.
//
// b is there so Close can wait for the bodies: detaching from the delivery must not also detach
// them from the process lifetime, or shutdown cuts a play off mid-frame and skips its cleanup.
func Bounded[T any](b *Bus, n int64, fn func(ctx context.Context, ev *T)) Handler[T] {
	sem := semaphore.NewWeighted(n)
	return func(ctx context.Context, ev *T) error {
		if err := sem.Acquire(ctx, 1); err != nil {
			return err // shutting down; the router will not redeliver
		}
		b.detached.Add(1)
		go func() { //nolint:gosec // G118: detached on purpose, see above
			defer b.detached.Done()
			defer sem.Release(1)
			// The router's Recoverer only covers the delivery, and this body has left it.
			defer func() {
				if r := recover(); r != nil {
					slog.Error("bus: detached handler panicked", slog.Any("panic", r), slog.String("stack", string(debug.Stack())))
				}
			}()
			fn(context.WithoutCancel(ctx), ev)
		}()
		return nil
	}
}

// Timeout gives a handler body its own deadline, derived from the context it is handed — so the
// parent's values, and any deadline already on it, still apply.
func Timeout[T any](d time.Duration, fn func(ctx context.Context, ev *T)) func(context.Context, *T) {
	return func(ctx context.Context, ev *T) {
		ctx, cancel := context.WithTimeout(ctx, d)
		defer cancel()
		fn(ctx, ev)
	}
}

// OnTopic registers a typed handler against a named topic on a subscriber the bus does not own.
// It is how a source outside the bus — the gateway wrapper — reaches the same router, and so the
// same correlation, tracing, metrics, recovery and retry as everything else.
//
// Topic and type are given rather than derived: the payload is Discord's shape, so there is no
// event name of ours to generate a topic from, and the two must not be allowed to drift apart by
// being inferred separately.
//
// What arrives here carries no bus envelope — the gateway writes Discord's payload as plain JSON
// and nothing else. Decoding still goes through the bus marshaler because its Unmarshal is
// encoding/json plus the validate pass, and a frame that came off the wire from Discord is exactly
// the untrusted input that pass exists for.
func OnTopic[T any](b *Bus, sub message.Subscriber, name, topic string, fn Handler[T]) {
	b.router.AddConsumerHandler(name, topic, sub, func(msg *message.Message) error {
		var ev T
		if err := b.marshaler.Unmarshal(msg, &ev); err != nil {
			return fmt.Errorf("bus: decode %s: %w", topic, err)
		}
		return fn(msg.Context(), &ev)
	})
}

// Run blocks until ctx is cancelled. Call after every On().
func (b *Bus) Run(ctx context.Context) error { return b.router.Run(ctx) }

// Running is closed once the router has started all subscribers — publish nothing before this.
func (b *Bus) Running() <-chan struct{} { return b.router.Running() }

// Close stops deliveries, waits for the handler bodies Bounded detached, then closes the publisher.
// The router owns the subscribers it was handed and closes them itself.
//
// The order is the point. The router goes first so nothing new detaches while we drain; the
// publisher goes last because a body still finishing can publish on its way out. ctx bounds the
// drain — the caller's shutdown budget is the bound, and a body outrunning it is reported rather
// than waited on, since the alternative is a process that will not die.
func (b *Bus) Close(ctx context.Context) error {
	err := b.router.Close()
	if !b.waitDetached(ctx) {
		slog.Warn("bus: shut down with detached handlers still running", slog.Any("err", ctx.Err()))
	}
	return errors.Join(err, b.pub.Close())
}

// waitDetached waits for the detached bodies, reporting whether they all finished before ctx did.
func (b *Bus) waitDetached(ctx context.Context) bool {
	done := make(chan struct{})
	// This outlives a ctx that fires first, but not the bodies: it ends when the last one does.
	go func() { defer close(done); b.detached.Wait() }()
	select {
	case <-done:
		return true
	case <-ctx.Done():
		return false
	}
}
