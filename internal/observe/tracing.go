package observe

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

// TracingShutdown stops tracing if it was started. It is a no-op when tracing is
// disabled.
type TracingShutdown func(ctx context.Context) error

// InitTracing configures an OTLP/HTTP trace exporter writing to endpoint and
// installs it as the global tracer provider. When endpoint is empty, tracing is
// disabled and the returned shutdown is a no-op. serviceName is attached as the
// OTel service.name attribute.
func InitTracing(endpoint, serviceName string, log *slog.Logger) (trace.Tracer, TracingShutdown) {
	if endpoint == "" {
		return otel.Tracer(serviceName), func(context.Context) error { return nil }
	}

	exporter, err := otlptracehttp.New(context.Background(),
		otlptracehttp.WithEndpointURL(endpoint),
		otlptracehttp.WithTimeout(10*time.Second),
	)
	if err != nil {
		log.Error("failed to create OTel exporter; tracing disabled", "error", err, "endpoint", endpoint)
		return otel.Tracer(serviceName), func(context.Context) error { return nil }
	}

	res, err := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(semconv.SchemaURL, semconv.ServiceName(serviceName)),
	)
	if err != nil {
		log.Warn("merge trace resource", "error", err)
		res = resource.Default()
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(1.0))),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})

	log.Info("OTel tracing enabled", "endpoint", endpoint, "service", serviceName)
	return tp.Tracer(serviceName), func(ctx context.Context) error {
		shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if err := tp.Shutdown(shutdownCtx); err != nil && !errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("shutdown tracer: %w", err)
		}
		return nil
	}
}
