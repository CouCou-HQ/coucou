package bus

import (
	"context"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/be-sandaa/coucou/internal/tracing"
)

// awaitSpan waits for a named span to be recorded as ended.
func awaitSpan(t *testing.T, rec *tracetest.SpanRecorder, name string) sdktrace.ReadOnlySpan {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, s := range rec.Ended() {
			if s.Name() == name {
				return s
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("span %q was never recorded", name)
	return nil
}

// A published event and the handler that consumes it must end up in one trace. That only works if
// the publisher writes the traceparent into the message metadata and the router reads it back —
// the two halves are wired separately, so this is what proves they meet.
func TestTraceSurvivesTheBus(t *testing.T) {
	rec := tracetest.NewSpanRecorder()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec)))
	otel.SetTextMapPropagator(propagation.TraceContext{})

	got := make(chan trace.SpanContext, 1)
	b := newTestBus(t, func(b *Bus) {
		On(b, "trace-probe", func(ctx context.Context, _ *SoundAdded) error {
			got <- trace.SpanContextFromContext(ctx)
			return nil
		})
	})

	ctx, root := tracing.Tracer().Start(context.Background(), "publisher")
	b.Publish(ctx, SoundAdded{Name: nameFirst})
	root.End()

	select {
	case sc := <-got:
		if !sc.IsValid() {
			t.Fatal("handler context carries no span — the traceparent never crossed the bus")
		}
		if sc.TraceID() != root.SpanContext().TraceID() {
			t.Errorf("handler trace %s, publisher trace %s — the consumer started its own trace",
				sc.TraceID(), root.SpanContext().TraceID())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("handler never ran")
	}

	// The consumer span must also be a real recorded span, not just an extracted remote context.
	// It ends after the handler returns, so poll rather than read once — reading straight after
	// the handler fired is a race that passes locally and fails under load.
	consumer := awaitSpan(t, rec, "bus consume trace-probe")
	if consumer.Parent().SpanID() != root.SpanContext().SpanID() {
		t.Errorf("consumer parent %s, want the publishing span %s",
			consumer.Parent().SpanID(), root.SpanContext().SpanID())
	}
	if consumer.SpanKind() != trace.SpanKindConsumer {
		t.Errorf("consumer span kind = %s, want consumer", consumer.SpanKind())
	}
}
