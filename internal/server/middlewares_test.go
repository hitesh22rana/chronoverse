//nolint:testpackage // Tests unexported middleware directly.
package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	oteltrace "go.opentelemetry.io/otel/trace"
	"go.uber.org/mock/gomock"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/hitesh22rana/chronoverse/internal/pkg/auth"
	authmock "github.com/hitesh22rana/chronoverse/internal/pkg/auth/mock"
	otelpkg "github.com/hitesh22rana/chronoverse/internal/pkg/otel"
)

func TestAttachAuthorizationTokenSeedsUserRole(t *testing.T) {
	authService := authmock.NewMockIAuth(gomock.NewController(t))
	authService.EXPECT().IssueToken(
		gomock.Any(), "user-42",
		auth.ServiceNameJobs,
	).DoAndReturn(func(ctx context.Context, _ string, _ ...string) (string, error) {
		role, err := auth.ExtractRoleFromContext(ctx)
		if err != nil || role != auth.RoleUser.String() {
			t.Fatalf("expected user role, got role=%q err=%v", role, err)
		}
		return "token", nil
	})

	s := &Server{auth: authService}
	handler := s.withAttachAuthorizationTokenInMetadataHeaderMiddleware(auth.ServiceNameJobs, http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusAccepted) },
	))
	req := httptest.NewRequest(http.MethodGet, "/jobs", http.NoBody)
	req = req.WithContext(context.WithValue(req.Context(), userIDKey{}, "user-42"))
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)

	if res.Code != http.StatusAccepted {
		t.Fatalf("expected status %d, got %d", http.StatusAccepted, res.Code)
	}
}

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
	if !strings.Contains(allowedHeaders, "x-csrf-token") {
		t.Fatalf("expected x-csrf-token in CORS allowed headers, got %q", allowedHeaders)
	}
}

func TestSecurityHeadersMiddleware(t *testing.T) {
	s := &Server{}
	handlerCalled := false
	handler := s.withSecurityHeadersMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusAccepted)
	}))

	res := httptest.NewRecorder()
	handler.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/health", http.NoBody))

	if !handlerCalled {
		t.Fatal("expected security headers middleware to call the next handler")
	}
	if res.Code != http.StatusAccepted {
		t.Fatalf("expected status %d, got %d", http.StatusAccepted, res.Code)
	}

	expectedHeaders := map[string]string{
		"Content-Security-Policy": "default-src 'none'; frame-ancestors 'none'",
		"Referrer-Policy":         "no-referrer",
		"X-Content-Type-Options":  "nosniff",
		"X-Frame-Options":         "DENY",
	}
	for name, expected := range expectedHeaders {
		if actual := res.Header().Get(name); actual != expected {
			t.Errorf("expected %s header %q, got %q", name, expected, actual)
		}
	}
}

func TestOtelMiddlewareLogsTraceIdentifiers(t *testing.T) {
	core, logs := observer.New(zapcore.InfoLevel)
	tracerProvider := sdktrace.NewTracerProvider(
		sdktrace.WithSpanProcessor(tracetest.NewSpanRecorder()),
	)
	s := &Server{
		logger: zap.New(core),
	}

	handler := otelpkg.HTTPHandler(
		s.withRequestLoggingMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			spanCtx := oteltrace.SpanContextFromContext(r.Context())
			if !spanCtx.IsValid() {
				t.Fatal("expected traced request context")
			}
			w.WriteHeader(http.StatusAccepted)
		})),
		"server-test",
		otelhttp.WithTracerProvider(tracerProvider),
	)

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

func TestRequestLoggingMiddlewarePreservesFlusher(t *testing.T) {
	s := &Server{logger: zap.NewNop()}
	flusherAvailable := false
	handler := s.withRequestLoggingMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, flusherAvailable = w.(http.Flusher)
		w.WriteHeader(http.StatusOK)
	}))

	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/events", http.NoBody))

	if !flusherAvailable {
		t.Fatal("expected request logging middleware to preserve http.Flusher")
	}
}

func TestVerifyCSRFMiddlewareRequiresHeader(t *testing.T) {
	s := &Server{validationCfg: &ValidationConfig{CSRFHMACSecret: "test-secret-0123456789abcdef", CSRFExpiry: time.Hour}}
	next := s.withVerifyCSRFMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	token, err := generateCSRFToken("sess", "test-secret-0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	newReq := func(header string) *http.Request {
		req := httptest.NewRequest(http.MethodPost, "/workflows", http.NoBody)
		req.AddCookie(&http.Cookie{Name: csrfCookieName, Value: token})
		req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "sess"})
		if header != "" {
			req.Header.Set(csrfHeaderName, header)
		}
		return req
	}

	res := httptest.NewRecorder()
	next.ServeHTTP(res, newReq(""))
	if res.Code != http.StatusForbidden {
		t.Fatalf("missing header: got %d, want 403", res.Code)
	}
	res = httptest.NewRecorder()
	next.ServeHTTP(res, newReq("wrong"))
	if res.Code != http.StatusForbidden {
		t.Fatalf("wrong header: got %d, want 403", res.Code)
	}
	res = httptest.NewRecorder()
	next.ServeHTTP(res, newReq(token))
	if res.Code != http.StatusOK {
		t.Fatalf("matching header: got %d, want 200", res.Code)
	}
}

func TestSecurityHeadersHSTSOnlyWhenSecure(t *testing.T) {
	for _, tc := range []struct {
		secure bool
		want   bool
	}{{false, false}, {true, true}} {
		s := &Server{hostConfig: &HostConfig{Secure: tc.secure}}
		h := s.withSecurityHeadersMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
		res := httptest.NewRecorder()
		h.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/", http.NoBody))
		if got := res.Header().Get("Strict-Transport-Security") != ""; got != tc.want {
			t.Fatalf("secure=%v: hsts present=%v", tc.secure, got)
		}
	}
}

func TestSetCookieIsHostOnly(t *testing.T) {
	recorder := httptest.NewRecorder()
	setCookie(recorder, sessionCookieName, "v", "example.com", true, time.Hour, http.SameSiteStrictMode)
	if d := recorder.Result().Cookies()[0].Domain; d != "" {
		t.Fatalf("expected empty Domain, got %q", d)
	}
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
