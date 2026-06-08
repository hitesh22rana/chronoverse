//nolint:testpackage // Tests unexported middleware directly.
package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	oteltrace "go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

func TestCORSMiddlewareAllowsIdempotencyKeyHeader(t *testing.T) {
	s := &Server{
		allowedOrigins: map[string]struct{}{
			"http://localhost:3001": {},
		},
	}

	handler := s.withCORSMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodOptions, "/workflows", http.NoBody)
	req.Header.Set("Origin", "http://localhost:3001")
	req.Header.Set("Access-Control-Request-Method", http.MethodPost)
	req.Header.Set("Access-Control-Request-Headers", "content-type, idempotency-key")

	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)

	if res.Code != http.StatusNoContent {
		t.Fatalf("expected status %d, got %d", http.StatusNoContent, res.Code)
	}

	allowedHeaders := strings.ToLower(res.Header().Get("Access-Control-Allow-Headers"))
	if !strings.Contains(allowedHeaders, "idempotency-key") {
		t.Fatalf("expected idempotency-key in CORS allowed headers, got %q", allowedHeaders)
	}
}

func TestOtelMiddlewareLogsTraceIdentifiers(t *testing.T) {
	core, logs := observer.New(zapcore.InfoLevel)
	tracerProvider := sdktrace.NewTracerProvider(
		sdktrace.WithSpanProcessor(tracetest.NewSpanRecorder()),
	)
	s := &Server{
		tp:     tracerProvider.Tracer("server-test"),
		logger: zap.New(core),
	}

	handler := s.withOtelMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		spanCtx := oteltrace.SpanContextFromContext(r.Context())
		if !spanCtx.IsValid() {
			t.Fatal("expected traced request context")
		}
		w.WriteHeader(http.StatusAccepted)
	}))

	req := httptest.NewRequest(http.MethodGet, "/notifications", http.NoBody)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)

	if res.Code != http.StatusAccepted {
		t.Fatalf("expected status %d, got %d", http.StatusAccepted, res.Code)
	}

	entries := logs.All()
	if len(entries) != 1 {
		t.Fatalf("expected 1 log entry, got %d", len(entries))
	}

	fields := entries[0].ContextMap()
	assertNonEmptyStringField(t, fields, "trace_id")
	assertNonEmptyStringField(t, fields, "span_id")
}

func assertNonEmptyStringField(t *testing.T, fields map[string]any, key string) {
	t.Helper()

	value, ok := fields[key]
	if !ok {
		t.Fatalf("expected %s field, got %#v", key, fields)
	}

	text, ok := value.(string)
	if !ok {
		t.Fatalf("expected %s field to be a string, got %T", key, value)
	}
	if text == "" {
		t.Fatalf("expected %s field to be non-empty, got %#v", key, fields)
	}
}
