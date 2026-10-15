//nolint:testpackage // Verifies HTTP cookies against real session storage.
package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"google.golang.org/grpc/metadata"
)

func TestIntegrationAuthSessionLifecycle(t *testing.T) {
	store := newSessionRedisStore(t.Context(), t)
	for _, register := range []bool{false, true} {
		name := "login"
		if register {
			name = "register"
		}
		t.Run(name, func(t *testing.T) {
			for _, secure := range []bool{false, true} {
				scheme := "http"
				if secure {
					scheme = "https"
				}
				t.Run(scheme, func(t *testing.T) {
					client := &sessionUsersClient{header: metadata.Pairs("authorization", "Bearer private.jwt.token")}
					server := &Server{
						usersClient: client, crypto: newTestCrypto(t), rdb: store,
						validationCfg: &ValidationConfig{SessionExpiry: time.Hour, CSRFExpiry: 15 * time.Minute, CSRFHMACSecret: testCSRFSecret},
						hostConfig:    &HostConfig{Secure: secure, SameSite: http.SameSiteStrictMode, CookieDomain: "example.com"},
					}
					response := httptest.NewRecorder()
					invokeAuthSession(server, register, response, authSessionRequest(register, `{"email":"alice@example.com","password":"secret"}`, "Application/JSON; charset=utf-8"))
					if response.Code != http.StatusCreated || response.Body.Len() != 0 {
						t.Fatalf("response = %d %q", response.Code, response.Body.String())
					}
					assertAuthSessionRPC(t, client, register)
					values := assertAuthSessionCookies(t, response, secure)
					session, csrf := values[sessionCookieName], values[csrfCookieName]
					if session == nil || csrf == nil || session.Value == "private.jwt.token" {
						t.Fatalf("invalid session cookies: %v", values)
					}
					decrypted, err := server.crypto.Decrypt(session.Value)
					if err != nil || decrypted != "private.jwt.token" {
						t.Fatalf("session decrypt = %q, %v", decrypted, err)
					}
					var userID string
					if _, err = store.Get(t.Context(), session.Value, &userID); err != nil || userID != testSessionUserID {
						t.Fatalf("stored identity = %q, %v", userID, err)
					}
					assertAuthSessionCSRF(t, csrf.Value, session.Value)
					if err = store.Delete(t.Context(), session.Value); err != nil {
						t.Fatal(err)
					}
				})
			}
		})
	}
}

func TestIntegrationAuthSessionPersistenceFailure(t *testing.T) {
	store := newSessionRedisStore(t.Context(), t)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	for _, register := range []bool{false, true} {
		name := "login"
		if register {
			name = "register"
		}
		t.Run(name, func(t *testing.T) {
			client := &sessionUsersClient{header: metadata.Pairs("authorization", "Bearer private.jwt.token")}
			server := &Server{usersClient: client, crypto: newTestCrypto(t), rdb: store, validationCfg: &ValidationConfig{SessionExpiry: time.Hour}}
			response := httptest.NewRecorder()
			invokeAuthSession(server, register, response, authSessionRequest(register, `{"email":"alice@example.com","password":"secret"}`, testJSONContentType))
			assertAuthSessionError(t, response, http.StatusInternalServerError, "failed to set session\n")
			assertAuthSessionRPC(t, client, register)
		})
	}
}

func assertAuthSessionCookies(t *testing.T, response *httptest.ResponseRecorder, secure bool) map[string]*http.Cookie {
	t.Helper()
	cookies := response.Result().Cookies()
	if len(cookies) != 2 {
		t.Fatalf("cookies = %v", cookies)
	}
	values := make(map[string]*http.Cookie)
	for _, cookie := range cookies {
		values[cookie.Name] = cookie
		expiry := time.Hour
		if cookie.Name == csrfCookieName {
			expiry = 15 * time.Minute
		}
		if cookie.Domain != "example.com" || cookie.Path != "/" || cookie.Secure != secure || cookie.SameSite != http.SameSiteStrictMode {
			t.Fatalf("cookie transport attributes = %+v", cookie)
		}
		if cookie.HttpOnly != (cookie.Name == sessionCookieName) || cookie.MaxAge != int(expiry.Seconds()) {
			t.Fatalf("cookie persistence attributes = %+v", cookie)
		}
	}
	return values
}

func assertAuthSessionCSRF(t *testing.T, csrf, session string) {
	t.Helper()
	if err := verifyCSRFToken(csrf, session, testCSRFSecret, 15*time.Minute); err != nil {
		t.Fatalf("CSRF token invalid: %v", err)
	}
	if err := verifyCSRFToken(csrf, session+"other", testCSRFSecret, 15*time.Minute); err == nil {
		t.Fatal("CSRF token accepted for another session")
	}
}
