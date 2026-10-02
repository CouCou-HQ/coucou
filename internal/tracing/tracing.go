// Package tracing sets up OpenTelemetry. It is off unless an OTLP endpoint is configured, and off
// means the global no-op tracer, so every Start call in the codebase stays valid and costs nothing.
package tracing

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/trace"
)

// Name is the instrumentation scope every span in coucou is created under.
const Name = "github.com/be-sandaa/coucou"

// Tracer is the one tracer the whole binary uses. Reading it at package level is safe: it resolves
// against the global provider on every call, so it picks up whatever Init installs later.
func Tracer() trace.Tracer { return otel.Tracer(Name) }

// Init installs a tracer provider exporting over OTLP/gRPC to endpoint, and returns its shutdown.
// An empty endpoint leaves the global no-op provider in place and returns a shutdown that does
// nothing, so a caller never has to branch on whether tracing is on.
//
// The propagator is installed either way. It is what reads and writes the traceparent in a bus
// message's metadata, and leaving it out would make a span silently lose its parent rather than
// fail — the kind of thing nobody notices until they need the trace.
func Init(ctx context.Context, endpoint, version string) (shutdown func(context.Context) error, err error) {
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{}, propagation.Baggage{},
	))
	if endpoint == "" {
		slog.Info("tracing disabled", slog.String("reason", "no -otlp-endpoint"))
		return func(context.Context) error { return nil }, nil
	}

	// Insecure because the collector is expected next door — a sidecar or a host on the same
	// network. Point this at anything further and it wants TLS.
	exp, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithEndpoint(endpoint),
		otlptracegrpc.WithInsecure(),
	)
	if err != nil {
		return nil, fmt.Errorf("otlp exporter: %w", err)
	}

	res, err := resource.Merge(resource.Default(), resource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceName("coucou"),
		semconv.ServiceVersion(version),
	))
	if err != nil {
		return nil, fmt.Errorf("otel resource: %w", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)
	slog.Info("tracing enabled", slog.String("endpoint", endpoint))

	return func(ctx context.Context) error {
		// Shutdown flushes the batch. Bound it, because a collector that has gone away would
		// otherwise hold up the whole process exit.
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		return tp.Shutdown(ctx)
	}, nil
}
