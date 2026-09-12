//nolint:testpackage // Tests unexported CSRF helpers directly.
package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestVerifyCSRFToken(t *testing.T) {
	const (
		session = "session-token"
		hmacKey = "test-hmac-key"
	)

	token, err := generateCSRFToken(session, hmacKey)
	if err != nil {
		t.Fatalf("generateCSRFToken() error = %v", err)
	}

	if err := verifyCSRFToken(token, session, hmacKey, time.Hour); err != nil {
		t.Fatalf("verifyCSRFToken() error = %v", err)
	}

	parts := strings.Split(token, delimiter)
	tamperedToken := strings.Repeat("0", len(parts[0])) + delimiter + parts[1]
	if err := verifyCSRFToken(tamperedToken, session, hmacKey, time.Hour); err == nil {
		t.Fatal("verifyCSRFToken() accepted a tampered token")
	}
}

func TestHandleGetCSRFToken(t *testing.T) {
	s := &Server{
		validationCfg: &ValidationConfig{CSRFHMACSecret: "test-secret-0123456789abcdef", CSRFExpiry: time.Hour},
		hostConfig:    &HostConfig{Host: "api.example.com", Secure: true, SameSite: http.SameSiteNoneMode},
	}

	unauth := httptest.NewRecorder()
	s.handleGetCSRFToken(unauth, httptest.NewRequest(http.MethodGet, "/auth/csrf", http.NoBody))
	if unauth.Code != http.StatusUnauthorized {
		t.Fatalf("missing session: got %d, want 401", unauth.Code)
	}

	req := httptest.NewRequest(http.MethodGet, "/auth/csrf", http.NoBody)
	req = req.WithContext(context.WithValue(req.Context(), sessionKey{}, "sess"))
	res := httptest.NewRecorder()
	s.handleGetCSRFToken(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", res.Code)
	}

	var body struct {
		CSRFToken string `json:"csrfToken"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil || body.CSRFToken == "" {
		t.Fatalf("missing csrfToken in body: %v", err)
	}
	if err := verifyCSRFToken(body.CSRFToken, "sess", "test-secret-0123456789abcdef", time.Hour); err != nil {
		t.Fatalf("returned token does not verify: %v", err)
	}
	setCookieHeader := res.Header().Get("Set-Cookie")
	if !strings.Contains(setCookieHeader, csrfCookieName+"=") {
		t.Fatalf("expected refreshed csrf cookie, got %q", setCookieHeader)
	}
	if strings.Contains(setCookieHeader, "HttpOnly") {
		t.Fatalf("csrf cookie must stay readable, got %q", setCookieHeader)
	}
}
