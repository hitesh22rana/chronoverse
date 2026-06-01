//nolint:testpackage // Tests unexported middleware directly.
package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
