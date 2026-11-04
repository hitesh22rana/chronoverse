package otel

import (
	"context"
	"errors"
	"os"
	"time"

	hostmetrics "go.opentelemetry.io/contrib/instrumentation/host"
	runtimemetrics "go.opentelemetry.io/contrib/instrumentation/runtime"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// InitResource initializes a resource with the given service name.
func InitResource(ctx context.Context, serviceName, serviceVersion string) (*resource.Resource, error) {
	hostName, err := os.Hostname()
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get hostname: %v", err)
	}

	res, err := resource.New(
		ctx,
		resource.WithFromEnv(),
		resource.WithProcess(),
		resource.WithContainer(),
		resource.WithAttributes(
			semconv.ServiceName(serviceName),
			semconv.ServiceVersion(serviceVersion),
			semconv.HostName(hostName),
		),
	)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to create resource: %v", err)
	}

	return res, nil
}

// InitTracerProvider initializes a new tracer provider with the OTLP gRPC exporter.
func InitTracerProvider(ctx context.Context, res *resource.Resource) (*sdktrace.TracerProvider, error) {
	exporter, err := otlptracegrpc.New(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to create OTLP trace exporter: %v", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)

	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	return tp, nil
}

// InitMeterProvider initializes a new meter provider with the OTLP gRPC exporter.
func InitMeterProvider(ctx context.Context, res *resource.Resource) (*sdkmetric.MeterProvider, error) {
	exporter, err := otlpmetricgrpc.New(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to create OTLP metric exporter: %v", err)
	}

	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(res),
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exporter)),
	)
	return registerMeterProvider(ctx, mp,
		func(mp *sdkmetric.MeterProvider) error {
			return hostmetrics.Start(hostmetrics.WithMeterProvider(mp))
		},
		func(mp *sdkmetric.MeterProvider) error {
			return runtimemetrics.Start(runtimemetrics.WithMeterProvider(mp))
		},
	)
}

// registerMeterProvider owns the provider until both instrumentation libraries
// have registered successfully. Failed startup closes its reader/exporter without
// publishing a partially initialized provider to the rest of the process.
func registerMeterProvider(ctx context.Context, mp *sdkmetric.MeterProvider, startHost, startRuntime func(*sdkmetric.MeterProvider) error) (*sdkmetric.MeterProvider, error) {
	rollback := func(err error) (*sdkmetric.MeterProvider, error) {
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		return nil, errors.Join(err, mp.Shutdown(shutdownCtx))
	}
	if err := startHost(mp); err != nil {
		return rollback(status.Errorf(codes.Internal, "failed to start hostmetrics: %v", err))
	}
	if err := startRuntime(mp); err != nil {
		return rollback(status.Errorf(codes.Internal, "failed to start runtime: %v", err))
	}
	otel.SetMeterProvider(mp)
	return mp, nil
}

// InitLogProvider initializes a new logger provider with the OTLP gRPC exporter.
func InitLogProvider(ctx context.Context, res *resource.Resource) (*sdklog.LoggerProvider, error) {
	exporter, err := otlploggrpc.New(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to create OTLP log exporter: %v", err)
	}

	lp := sdklog.NewLoggerProvider(
		sdklog.WithResource(res),
		sdklog.WithProcessor(sdklog.NewBatchProcessor(exporter)),
	)

	return lp, nil
}
