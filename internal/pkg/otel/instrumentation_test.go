package otel_test

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/test/bufconn"

	otelpkg "github.com/hitesh22rana/chronoverse/internal/pkg/otel"
)

func TestHTTPHandlerRecordsRouteMetrics(t *testing.T) {
	t.Parallel()

	reader := sdkmetric.NewManualReader()
	meterProvider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	spanRecorder := tracetest.NewSpanRecorder()
	tracerProvider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spanRecorder))

	mux := http.NewServeMux()
	mux.HandleFunc("GET /workflows/{workflow_id}", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		if _, err := io.WriteString(w, "accepted"); err != nil {
			t.Fatalf("failed to write response: %v", err)
		}
	})
	handler := otelpkg.HTTPHandler(
		mux,
		"test-server",
		otelhttp.WithMeterProvider(meterProvider),
		otelhttp.WithTracerProvider(tracerProvider),
	)

	request := httptest.NewRequest(http.MethodGet, "/workflows/workflow-123", http.NoBody)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusAccepted {
		t.Fatalf("expected status %d, got %d", http.StatusAccepted, response.Code)
	}

	duration := collectFloatHistogram(t, reader, "http.server.request.duration")
	if len(duration.DataPoints) != 1 {
		t.Fatalf("expected one duration datapoint, got %d", len(duration.DataPoints))
	}

	attrs := duration.DataPoints[0].Attributes
	assertStringAttribute(t, attrs, "http.request.method", http.MethodGet)
	assertStringAttribute(t, attrs, "http.route", "/workflows/{workflow_id}")
	assertInt64Attribute(t, attrs, "http.response.status_code", http.StatusAccepted)
	if strings.Contains(attrs.Encoded(attribute.DefaultEncoder()), "workflow-123") {
		t.Fatal("metric attributes contain a concrete workflow ID")
	}

	assertMetricExists(t, reader, "http.server.request.body.size")
	assertMetricExists(t, reader, "http.server.response.body.size")

	spans := spanRecorder.Ended()
	if len(spans) != 1 || spans[0].Name() != "GET /workflows/{workflow_id}" {
		t.Fatalf("unexpected spans: %#v", spans)
	}
}

func TestHTTPHandlerRecordsErrorAndUnmatchedRequests(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		method     string
		target     string
		wantStatus int
	}{
		{name: "method not allowed", method: http.MethodPost, target: "/known", wantStatus: http.StatusMethodNotAllowed},
		{name: "unmatched", method: http.MethodGet, target: "/missing", wantStatus: http.StatusNotFound},
		{name: "server error", method: http.MethodGet, target: "/error", wantStatus: http.StatusInternalServerError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			reader := sdkmetric.NewManualReader()
			meterProvider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
			mux := http.NewServeMux()
			mux.HandleFunc("GET /known", func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
			})
			mux.HandleFunc("GET /error", func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, "failed", http.StatusInternalServerError)
			})
			handler := otelpkg.HTTPHandler(mux, "test-server", otelhttp.WithMeterProvider(meterProvider))

			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(tt.method, tt.target, http.NoBody))
			if response.Code != tt.wantStatus {
				t.Fatalf("expected status %d, got %d", tt.wantStatus, response.Code)
			}

			duration := collectFloatHistogram(t, reader, "http.server.request.duration")
			if len(duration.DataPoints) != 1 {
				t.Fatalf("expected one duration datapoint, got %d", len(duration.DataPoints))
			}
			assertInt64Attribute(t, duration.DataPoints[0].Attributes, "http.response.status_code", tt.wantStatus)
		})
	}
}

func TestHTTPHandlerPreservesStreamingAndDownloadWriters(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		pattern string
		target  string
		route   string
	}{
		{name: "event stream", pattern: "GET /workflows/{workflow_id}/jobs/{job_id}/events", target: "/workflows/wf-1/jobs/job-1/events", route: "/workflows/{workflow_id}/jobs/{job_id}/events"},
		{name: "log download", pattern: "GET /workflows/{workflow_id}/jobs/{job_id}/logs/raw", target: "/workflows/wf-1/jobs/job-1/logs/raw", route: "/workflows/{workflow_id}/jobs/{job_id}/logs/raw"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			reader := sdkmetric.NewManualReader()
			meterProvider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
			mux := http.NewServeMux()
			mux.HandleFunc(tt.pattern, func(w http.ResponseWriter, _ *http.Request) {
				flusher, ok := w.(http.Flusher)
				if !ok {
					t.Fatal("instrumented response writer does not implement http.Flusher")
				}
				if _, err := io.WriteString(w, "chunk"); err != nil {
					t.Fatalf("failed to write stream chunk: %v", err)
				}
				flusher.Flush()
			})
			handler := otelpkg.HTTPHandler(mux, "test-server", otelhttp.WithMeterProvider(meterProvider))

			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, tt.target, http.NoBody))

			duration := collectFloatHistogram(t, reader, "http.server.request.duration")
			if len(duration.DataPoints) != 1 || duration.DataPoints[0].Count != 1 {
				t.Fatalf("expected one completed request measurement, got %#v", duration.DataPoints)
			}
			assertStringAttribute(t, duration.DataPoints[0].Attributes, "http.route", tt.route)
		})
	}
}

func TestHTTPTransportPropagatesTraceAndRecordsMetrics(t *testing.T) {
	t.Parallel()

	reader := sdkmetric.NewManualReader()
	meterProvider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	tracerProvider := sdktrace.NewTracerProvider()
	traceparent := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		traceparent <- r.Header.Get("traceparent")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client := &http.Client{Transport: otelpkg.HTTPTransport(
		nil,
		otelhttp.WithMeterProvider(meterProvider),
		otelhttp.WithTracerProvider(tracerProvider),
		otelhttp.WithPropagators(propagation.TraceContext{}),
	)}
	ctx, span := tracerProvider.Tracer("test").Start(context.Background(), "parent")
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, http.NoBody)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	response, err := client.Do(request)
	span.End()
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	if err := response.Body.Close(); err != nil {
		t.Fatalf("failed to close response body: %v", err)
	}

	if got := <-traceparent; got == "" {
		t.Fatal("expected traceparent header")
	}
	assertMetricExists(t, reader, "http.client.request.duration")
}

func TestGRPCHandlersRecordUnaryAndStreamingMetrics(t *testing.T) {
	t.Parallel()

	serverReader := sdkmetric.NewManualReader()
	serverMeterProvider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(serverReader))
	clientReader := sdkmetric.NewManualReader()
	clientMeterProvider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(clientReader))
	tracerProvider := sdktrace.NewTracerProvider()

	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer(grpc.StatsHandler(otelpkg.GRPCServerHandler(
		otelgrpc.WithMeterProvider(serverMeterProvider),
		otelgrpc.WithTracerProvider(tracerProvider),
	)))
	healthServer := health.NewServer()
	healthServer.SetServingStatus("test", grpc_health_v1.HealthCheckResponse_SERVING)
	grpc_health_v1.RegisterHealthServer(server, healthServer)
	serveErr := make(chan error, 1)
	go func() {
		serveErr <- server.Serve(listener)
	}()
	defer func() {
		server.Stop()
		if err := <-serveErr; err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			t.Errorf("gRPC server failed: %v", err)
		}
	}()

	conn, err := grpc.NewClient(
		"passthrough:///test",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return listener.Dial()
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithStatsHandler(otelpkg.GRPCClientHandler(
			otelgrpc.WithMeterProvider(clientMeterProvider),
			otelgrpc.WithTracerProvider(tracerProvider),
		)),
	)
	if err != nil {
		t.Fatalf("failed to create gRPC client: %v", err)
	}
	defer conn.Close()
	client := grpc_health_v1.NewHealthClient(conn)

	if _, checkErr := client.Check(t.Context(), &grpc_health_v1.HealthCheckRequest{Service: "test"}); checkErr != nil {
		t.Fatalf("unary health check failed: %v", checkErr)
	}
	streamCtx, cancel := context.WithCancel(t.Context())
	stream, err := client.Watch(streamCtx, &grpc_health_v1.HealthCheckRequest{Service: "test"})
	if err != nil {
		cancel()
		t.Fatalf("streaming health check failed: %v", err)
	}
	if _, err := stream.Recv(); err != nil {
		cancel()
		t.Fatalf("failed to receive streaming health response: %v", err)
	}
	cancel()
	if _, recvErr := stream.Recv(); recvErr == nil {
		t.Fatal("expected canceled stream to return an error")
	}
	time.Sleep(50 * time.Millisecond)

	serverDuration := collectFloatHistogram(t, serverReader, "rpc.server.call.duration")
	clientDuration := collectFloatHistogram(t, clientReader, "rpc.client.call.duration")
	assertRPCMethods(t, serverDuration.DataPoints, "Check", "Watch")
	assertRPCMethods(t, clientDuration.DataPoints, "Check", "Watch")
}

func collectFloatHistogram(t *testing.T, reader *sdkmetric.ManualReader, name string) metricdata.Histogram[float64] {
	t.Helper()

	metrics := collectMetrics(t, reader)
	for _, scope := range metrics.ScopeMetrics {
		for _, metric := range scope.Metrics {
			if metric.Name == name {
				histogram, ok := metric.Data.(metricdata.Histogram[float64])
				if !ok {
					t.Fatalf("metric %s has type %T", name, metric.Data)
				}
				return histogram
			}
		}
	}

	t.Fatalf("metric %s not found", name)
	return metricdata.Histogram[float64]{}
}

func assertMetricExists(t *testing.T, reader *sdkmetric.ManualReader, name string) {
	t.Helper()

	metrics := collectMetrics(t, reader)
	for _, scope := range metrics.ScopeMetrics {
		for _, metric := range scope.Metrics {
			if metric.Name == name {
				return
			}
		}
	}

	t.Fatalf("metric %s not found", name)
}

func collectMetrics(t *testing.T, reader *sdkmetric.ManualReader) metricdata.ResourceMetrics {
	t.Helper()

	var metrics metricdata.ResourceMetrics
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := reader.Collect(ctx, &metrics); err != nil {
		t.Fatalf("failed to collect metrics: %v", err)
	}
	return metrics
}

func assertStringAttribute(t *testing.T, attrs attribute.Set, key, want string) {
	t.Helper()

	value, ok := attrs.Value(attribute.Key(key))
	if !ok || value.AsString() != want {
		t.Fatalf("expected attribute %s=%q, got %v", key, want, value)
	}
}

func assertInt64Attribute(t *testing.T, attrs attribute.Set, key string, want int) {
	t.Helper()

	value, ok := attrs.Value(attribute.Key(key))
	if !ok || value.AsInt64() != int64(want) {
		t.Fatalf("expected attribute %s=%d, got %v", key, want, value)
	}
}

func assertRPCMethods(t *testing.T, points []metricdata.HistogramDataPoint[float64], methods ...string) {
	t.Helper()

	found := make(map[string]bool, len(methods))
	for _, point := range points {
		value, ok := point.Attributes.Value(attribute.Key("rpc.method"))
		if ok {
			for _, method := range methods {
				if strings.HasSuffix(value.AsString(), "/"+method) {
					found[method] = true
				}
			}
		}
	}
	for _, method := range methods {
		if !found[method] {
			t.Fatalf("RPC method %s not found in metric datapoints", method)
		}
	}
}
