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

	setCookie(recorder, sessionCookieName, "", "localhost", false, -1, http.SameSiteStrictMode)

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

func TestSetCookieKeepsPositiveDuration(t *testing.T) {
	recorder := httptest.NewRecorder()

	setCookie(recorder, sessionCookieName, "value", "localhost", false, time.Hour, http.SameSiteStrictMode)

	cookies := recorder.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("expected one cookie, got %d", len(cookies))
	}
	if cookies[0].MaxAge != 3600 {
		t.Fatalf("expected MaxAge 3600, got %d", cookies[0].MaxAge)
	}
}
