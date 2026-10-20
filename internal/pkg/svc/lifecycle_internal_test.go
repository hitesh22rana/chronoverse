package svc

import (
	"context"
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	collectorlog "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	collectormetric "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/grpc"

	loggerpkg "github.com/hitesh22rana/chronoverse/internal/pkg/logger"
)

type lifecycleCollector struct {
	mu                    sync.Mutex
	logs, metrics, traces int
}

// The services have the same Export name, so each adapter delegates to the collector.
type lifecycleLogCollector struct {
	collectorlog.UnimplementedLogsServiceServer
	collector *lifecycleCollector
}

func (c lifecycleLogCollector) Export(context.Context, *collectorlog.ExportLogsServiceRequest) (*collectorlog.ExportLogsServiceResponse, error) {
	c.collector.mu.Lock()
	defer c.collector.mu.Unlock()
	c.collector.logs++
	return &collectorlog.ExportLogsServiceResponse{}, nil
}

type lifecycleMetricCollector struct {
	collectormetric.UnimplementedMetricsServiceServer
	collector *lifecycleCollector
}

func (c lifecycleMetricCollector) Export(context.Context, *collectormetric.ExportMetricsServiceRequest) (*collectormetric.ExportMetricsServiceResponse, error) {
	c.collector.mu.Lock()
	defer c.collector.mu.Unlock()
	c.collector.metrics++
	return &collectormetric.ExportMetricsServiceResponse{}, nil
}

type lifecycleTraceCollector struct {
	collectortrace.UnimplementedTraceServiceServer
	collector *lifecycleCollector
}

func (c lifecycleTraceCollector) Export(context.Context, *collectortrace.ExportTraceServiceRequest) (*collectortrace.ExportTraceServiceResponse, error) {
	c.collector.mu.Lock()
	defer c.collector.mu.Unlock()
	c.collector.traces++
	return &collectortrace.ExportTraceServiceResponse{}, nil
}

func restoreServiceMetadata(t *testing.T) {
	t.Helper()
	oldSvc, oldName, oldVersion, oldPrivate, oldPublic := svc, name, version, authPrivateKeyPath, authPublicKeyPath
	t.Cleanup(func() {
		svc, name, version, authPrivateKeyPath, authPublicKeyPath = oldSvc, oldName, oldVersion, oldPrivate, oldPublic
	})
	svc = Svc{}
	name = "test-service"
	version = "test-version"
	authPrivateKeyPath = "test-private.pem"
	authPublicKeyPath = "test-public.pem"
}

func TestServiceMetadataInitializedOnce(t *testing.T) {
	restoreServiceMetadata(t)
	initSvcInfo()
	got := Info()
	require.Equal(t, "test-service", got.GetName())
	require.Equal(t, "test-version", got.GetVersion())
	require.Equal(t, "test-private.pem", got.GetAuthPrivateKeyPath())
	require.Equal(t, "test-public.pem", got.GetAuthPublicKeyPath())
	name = "changed"
	version = "changed"
	authPrivateKeyPath = "changed"
	authPublicKeyPath = "changed"
	initSvcInfo()
	require.Equal(t, got, Info())
}

func TestInitFlushesProvidersAndCancelsOnce(t *testing.T) {
	restoreServiceMetadata(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	collector := &lifecycleCollector{}
	server := grpc.NewServer()
	collectorlog.RegisterLogsServiceServer(server, lifecycleLogCollector{collector: collector})
	collectormetric.RegisterMetricsServiceServer(server, lifecycleMetricCollector{collector: collector})
	collectortrace.RegisterTraceServiceServer(server, lifecycleTraceCollector{collector: collector})
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	t.Cleanup(func() { server.Stop(); require.NoError(t, <-done) })
	endpoint := "http://" + listener.Addr().String()
	for _, key := range []string{"OTEL_EXPORTER_OTLP_ENDPOINT", "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", "OTEL_EXPORTER_OTLP_LOGS_ENDPOINT"} {
		t.Setenv(key, endpoint)
	}
	for _, key := range []string{"OTEL_EXPORTER_OTLP_HEADERS", "OTEL_EXPORTER_OTLP_TRACES_HEADERS", "OTEL_EXPORTER_OTLP_METRICS_HEADERS", "OTEL_EXPORTER_OTLP_LOGS_HEADERS", "OTEL_RESOURCE_ATTRIBUTES"} {
		t.Setenv(key, "")
	}
	for _, key := range []string{"OTEL_EXPORTER_OTLP_INSECURE", "OTEL_EXPORTER_OTLP_TRACES_INSECURE", "OTEL_EXPORTER_OTLP_METRICS_INSECURE", "OTEL_EXPORTER_OTLP_LOGS_INSECURE"} {
		t.Setenv(key, "true")
	}
	t.Setenv("OTEL_TRACES_SAMPLER", "always_on")
	oldTracer, oldMeter, oldPropagator := otel.GetTracerProvider(), otel.GetMeterProvider(), otel.GetTextMapPropagator()
	t.Cleanup(func() {
		otel.SetTracerProvider(oldTracer)
		otel.SetMeterProvider(oldMeter)
		otel.SetTextMapPropagator(oldPropagator)
	})
	ctx, shutdown := Init()
	t.Cleanup(shutdown)
	require.NoError(t, ctx.Err())
	require.Equal(t, "test-service", Info().GetName())
	require.NotNil(t, loggerpkg.FromContext(ctx))
	_, span := otel.Tracer("service-test").Start(ctx, "flush-on-shutdown")
	span.End()
	counter, err := otel.Meter("service-test").Int64Counter("shutdown_test_calls")
	require.NoError(t, err)
	counter.Add(ctx, 1)
	loggerpkg.FromContext(ctx).Info("flush-on-shutdown")
	var callers sync.WaitGroup
	for range 4 {
		callers.Go(shutdown)
	}
	callers.Wait()
	require.ErrorIs(t, ctx.Err(), context.Canceled)
	shutdown()
	collector.mu.Lock()
	defer collector.mu.Unlock()
	require.Positive(t, collector.logs)
	require.Positive(t, collector.metrics)
	require.Positive(t, collector.traces)
}

func TestInitRejectsMalformedResource(t *testing.T) {
	if os.Getenv("CHRONOVERSE_TEST_SVC_BAD_RESOURCE") == "1" {
		t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "missing-value")
		var got any
		func() {
			defer func() { got = recover() }()
			Init()
		}()
		require.Contains(t, fmt.Sprint(got), "failed to create resource")
		require.Contains(t, fmt.Sprint(got), "missing value")
		return
	}
	// Init panics before returning its signal cleanup on invalid startup config.
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	args := []string{"-test.run=^TestInitRejectsMalformedResource$"}
	if directory := flag.Lookup("test.gocoverdir").Value.String(); directory != "" {
		args = append(args, "-test.gocoverdir="+directory)
	}
	//nolint:gosec // Runs only this test binary with fixed helper flags.
	cmd := exec.CommandContext(ctx, os.Args[0], args...)
	cmd.Env = append(os.Environ(), "CHRONOVERSE_TEST_SVC_BAD_RESOURCE=1")
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "resource failure helper: %s", output)
}
