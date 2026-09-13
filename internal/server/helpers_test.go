//nolint:testpackage // Tests unexported cookie helper directly.
package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSetCookieDeletesExpiredCookie(t *testing.T) {
	recorder := httptest.NewRecorder()

	setCookie(recorder, sessionCookieName, "", "localhost", false, true, -1, http.SameSiteStrictMode)

	cookies := recorder.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("expected one cookie, got %d", len(cookies))
	}

	cookie := cookies[0]
	if cookie.MaxAge != -1 {
		t.Fatalf("expected MaxAge -1 for deletion, got %d", cookie.MaxAge)
	}
	if !cookie.Expires.Equal(time.Unix(0, 0).UTC()) {
		t.Fatalf("expected unix epoch expiry, got %s", cookie.Expires)
	}
	if !strings.Contains(recorder.Header().Get("Set-Cookie"), "Max-Age=0") {
		t.Fatalf("expected Set-Cookie deletion header, got %q", recorder.Header().Get("Set-Cookie"))
	}
}

func TestDecodeJSONRequestRequiresJSONContentType(t *testing.T) {
	newRequest := func(contentType, body string) *http.Request {
		req := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(body))
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		return req
	}

	var req struct {
		Email string `json:"email"`
	}

	if err := decodeJSONRequest(newRequest("text/plain", `{"email":"a@b.c"}`), &req); err == nil {
		t.Fatal("text/plain body was accepted")
	}
	if err := decodeJSONRequest(newRequest("", `{"email":"a@b.c"}`), &req); err == nil {
		t.Fatal("missing content-type was accepted")
	}
	if err := decodeJSONRequest(newRequest("application/json; charset=utf-8", `{"email":"a@b.c"}`), &req); err != nil {
		t.Fatalf("json content-type was rejected: %v", err)
	}
}

func TestSetCookieKeepsPositiveDuration(t *testing.T) {
	recorder := httptest.NewRecorder()

	setCookie(recorder, sessionCookieName, "value", "localhost", false, true, time.Hour, http.SameSiteStrictMode)

	cookies := recorder.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("expected one cookie, got %d", len(cookies))
	}
	if cookies[0].MaxAge != 3600 {
		t.Fatalf("expected MaxAge 3600, got %d", cookies[0].MaxAge)
	}
}
