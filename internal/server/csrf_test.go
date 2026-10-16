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

func TestGenerateHMAC(t *testing.T) {
	t.Parallel()
	// RFC 4231, test case 2.
	const want = "5bdcc146bf60754e6a042426089575c75a003f089d2739839dec58b964ec3843"
	if got := generateHMAC("what do ya want for nothing?", "Jefe"); got != want {
		t.Fatalf("HMAC = %q; want %q", got, want)
	}
}

func TestVerifyCSRFToken(t *testing.T) {
	const (
		session = "session-token"
		hmacKey = "test-hmac-key"
	)

	token := generateCSRFToken(session, hmacKey)

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

func TestHandleGetCSRFTokenReusesValidCookie(t *testing.T) {
	s := &Server{
		validationCfg: &ValidationConfig{CSRFHMACSecret: "test-secret-0123456789abcdef", CSRFExpiry: time.Hour},
		hostConfig:    &HostConfig{Host: "api.example.com", Secure: true, SameSite: http.SameSiteNoneMode},
	}

	token := generateCSRFToken("sess", "test-secret-0123456789abcdef")

	req := httptest.NewRequest(http.MethodGet, "/auth/csrf", http.NoBody)
	req = req.WithContext(context.WithValue(req.Context(), sessionKey{}, "sess"))
	req.AddCookie(&http.Cookie{Name: csrfCookieName, Value: token, Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode})
	res := httptest.NewRecorder()
	s.handleGetCSRFToken(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", res.Code)
	}

	var body struct {
		CSRFToken string `json:"csrfToken"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.CSRFToken != token {
		t.Fatal("valid presented token must be reused, not rotated")
	}
	if setCookie := res.Header().Get("Set-Cookie"); setCookie != "" {
		t.Fatalf("reused token must not overwrite the cookie, got %q", setCookie)
	}
}
