//nolint:testpackage // Tests the unexported session middleware against a real Redis store.
package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go/modules/redis"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/hitesh22rana/chronoverse/internal/pkg/auth"
	authmock "github.com/hitesh22rana/chronoverse/internal/pkg/auth/mock"
	redispkg "github.com/hitesh22rana/chronoverse/internal/pkg/redis"
	"github.com/hitesh22rana/chronoverse/internal/pkg/testkit"
)

const (
	testRedisImage = "redis:8.2.1-alpine"
	// testSessionUserID is the identity every seeded session maps to.
	testSessionUserID = "user-42"
	// testContainerStartTimeout bounds the container start, including the
	// image pull a cold cache would trigger.
	testContainerStartTimeout = 60 * time.Second
	// testContainerStopTimeout bounds teardown. Go cancels t.Context() before
	// running cleanup callbacks, so the terminate call needs its own live
	// context instead of the one the caller handed in.
	testContainerStopTimeout = 15 * time.Second
)

// newSessionRedisStore starts a throwaway Redis and returns a store wired with
// the same production settings the server uses. Container start problems skip
// outside strict mode and fail under TESTKIT_STRICT.
func newSessionRedisStore(ctx context.Context, t *testing.T) *redispkg.Store {
	t.Helper()
	//nolint:contextcheck // RequireDocker mints its own ping context; no caller ctx to propagate.
	testkit.RequireDocker(t)

	// Bounded startup so a wedged pull fails the test instead of stalling it,
	// while still deriving from the caller so cancellation propagates.
	startCtx, cancelStart := context.WithTimeout(ctx, testContainerStartTimeout)
	defer cancelStart()

	ctr, err := redis.Run(startCtx, testRedisImage)
	if err != nil {
		t.Fatalf("start redis container: %v", err)
	}
	//nolint:contextcheck // The incoming ctx is already canceled when cleanups run.
	t.Cleanup(func() {
		// t.Context() is already canceled here, so Terminate gets a fresh
		// bounded context; handing it the dead one made Stop fail and left the
		// container running until the testcontainers reaper noticed.
		termCtx, cancelTerm := context.WithTimeout(context.Background(), testContainerStopTimeout)
		defer cancelTerm()

		if termErr := ctr.Terminate(termCtx); termErr != nil {
			t.Errorf("terminate redis container: %v", termErr)
		}
	})

	host, hostErr := ctr.Host(ctx)
	if hostErr != nil {
		t.Fatalf("container host: %v", hostErr)
	}
	mapped, portErr := ctr.MappedPort(ctx, "6379/tcp")
	if portErr != nil {
		t.Fatalf("container port: %v", portErr)
	}
	portNum, atoiErr := strconv.Atoi(mapped.Port())
	if atoiErr != nil {
		t.Fatalf("container port number: %v", atoiErr)
	}

	store, err := redispkg.New(ctx, &redispkg.Config{
		Host:                     host,
		Port:                     portNum,
		DB:                       0,
		PoolSize:                 5,
		MinIdleConns:             1,
		ReadTimeout:              3 * time.Second,
		WriteTimeout:             3 * time.Second,
		MaxMemory:                "100mb",
		EvictionPolicy:           "volatile-ttl",
		EvictionPolicySampleSize: 5,
		TLSConfig:                &redispkg.TLSConfig{Enabled: false},
	})
	if err != nil {
		t.Fatalf("connect redis: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})
	return store
}

// newAcceptingAuth returns an auth double that validates any presented token.
func newAcceptingAuth(t *testing.T) auth.IAuth {
	t.Helper()

	authService := authmock.NewMockIAuth(gomock.NewController(t))
	authService.EXPECT().
		ValidateToken(gomock.Any(), gomock.Any()).
		DoAndReturn(func(ctx context.Context, _ string) (context.Context, any, error) {
			return ctx, nil, nil
		}).
		AnyTimes()
	return authService
}

// mintSession stores a session in Redis exactly the way login/register does and
// returns the cookie value the client would receive.
func mintSession(ctx context.Context, t *testing.T, store *redispkg.Store, s *Server) string {
	t.Helper()

	session, err := s.crypto.Encrypt("valid.jwt.token")
	if err != nil {
		t.Fatalf("encrypt session: %v", err)
	}
	if err := store.Set(ctx, session, testSessionUserID, time.Hour); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	return session
}

// TestIntegrationVerifySessionMiddlewareRequiresRedisSession proves a cookie
// that decrypts and passes token validation is still refused when Redis no
// longer holds the session. This is the replay path after logout or session
// expiry: the JWT itself stays cryptographically valid.
func TestIntegrationVerifySessionMiddlewareRequiresRedisSession(t *testing.T) {
	ctx := t.Context()
	store := newSessionRedisStore(ctx, t)

	s := &Server{auth: newAcceptingAuth(t), crypto: newTestCrypto(t), rdb: store}

	session, err := s.crypto.Encrypt("valid.jwt.token")
	if err != nil {
		t.Fatalf("encrypt session: %v", err)
	}

	nextCalled := false
	handler := s.withVerifySessionMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusOK)
	}))

	t.Run("missing redis session", func(t *testing.T) {
		nextCalled = false

		req := httptest.NewRequest(http.MethodGet, "/notifications", http.NoBody)
		req.AddCookie(secureTestCookie(sessionCookieName, session))

		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)

		if res.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want %d", res.Code, http.StatusUnauthorized)
		}
		if got := res.Body.String(); got != "invalid auth token\n" {
			t.Fatalf("body = %q, want %q", got, "invalid auth token\n")
		}
		if nextCalled {
			t.Fatal("next handler ran for a session Redis does not know")
		}
	})

	t.Run("session present in redis", func(t *testing.T) {
		nextCalled = false

		live := mintSession(ctx, t, store, s)

		req := httptest.NewRequest(http.MethodGet, "/notifications", http.NoBody)
		req.AddCookie(secureTestCookie(sessionCookieName, live))

		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)

		if res.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d (body %q)", res.Code, http.StatusOK, res.Body.String())
		}
		if !nextCalled {
			t.Fatal("next handler did not run for a live session")
		}
	})
}

// TestIntegrationVerifySessionMiddlewarePropagatesUserID proves the identity
// used downstream comes from Redis, not from the client-supplied token, so a
// tampered token subject cannot impersonate another user.
func TestIntegrationVerifySessionMiddlewarePropagatesUserID(t *testing.T) {
	ctx := t.Context()
	store := newSessionRedisStore(ctx, t)

	s := &Server{auth: newAcceptingAuth(t), crypto: newTestCrypto(t), rdb: store}
	session := mintSession(ctx, t, store, s)

	var observedUserID string
	var observedSession string
	handler := s.withVerifySessionMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var err error
		observedUserID, err = userIDFromContext(r.Context())
		if err != nil {
			t.Errorf("downstream user id: %v", err)
		}
		observedSession, err = sessionFromContext(r.Context())
		if err != nil {
			t.Errorf("downstream session: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/notifications", http.NoBody)
	req.AddCookie(secureTestCookie(sessionCookieName, session))

	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusOK)
	}
	if observedUserID != testSessionUserID {
		t.Fatalf("downstream user id = %q, want %s", observedUserID, testSessionUserID)
	}
	if observedSession != session {
		t.Fatalf("downstream session = %q, want the cookie value", observedSession)
	}
}

// TestIntegrationLogoutRevokesSession proves logout removes the Redis session
// so the very cookie it just cleared can no longer authenticate. The cookies are
// cleared on every logout, so the revocation has to happen for real.
func TestIntegrationLogoutRevokesSession(t *testing.T) {
	ctx := t.Context()
	store := newSessionRedisStore(ctx, t)

	s := &Server{
		auth:          newAcceptingAuth(t),
		crypto:        newTestCrypto(t),
		rdb:           store,
		hostConfig:    &HostConfig{CookieDomain: "example.com", Secure: true, SameSite: http.SameSiteStrictMode},
		validationCfg: &ValidationConfig{SessionExpiry: time.Hour, CSRFExpiry: time.Hour, CSRFHMACSecret: testCSRFSecret},
	}

	session := mintSession(ctx, t, store, s)

	verify := s.withVerifySessionMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	before := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/notifications", http.NoBody)
	req.AddCookie(secureTestCookie(sessionCookieName, session))
	verify.ServeHTTP(before, req)
	if before.Code != http.StatusOK {
		t.Fatalf("pre-logout status = %d, want %d", before.Code, http.StatusOK)
	}

	logout := httptest.NewRecorder()
	logoutReq := httptest.NewRequest(http.MethodPost, "/auth/logout", http.NoBody).
		WithContext(context.WithValue(ctx, sessionKey{}, session))
	s.handleLogout(logout, logoutReq)

	if logout.Code != http.StatusNoContent {
		t.Fatalf("logout status = %d, want %d (body %q)", logout.Code, http.StatusNoContent, logout.Body.String())
	}
	assertClearedCookies(t, logout)

	after := httptest.NewRecorder()
	afterReq := httptest.NewRequest(http.MethodGet, "/notifications", http.NoBody)
	afterReq.AddCookie(secureTestCookie(sessionCookieName, session))
	verify.ServeHTTP(after, afterReq)

	if after.Code != http.StatusUnauthorized {
		t.Fatalf("post-logout status = %d, want %d (body %q)", after.Code, http.StatusUnauthorized, after.Body.String())
	}
	if got := after.Body.String(); got != "invalid auth token\n" {
		t.Fatalf("post-logout body = %q, want %q", got, "invalid auth token\n")
	}
}

// secureTestCookie builds a request cookie with the attributes production sets,
// so gosec sees the same hardened shape the server emits.
func secureTestCookie(name, value string) *http.Cookie {
	return &http.Cookie{
		Name:     name,
		Value:    value,
		Secure:   true,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	}
}

// userIDFromContext reads the downstream identity the session middleware set.
func userIDFromContext(ctx context.Context) (string, error) {
	userID, ok := ctx.Value(userIDKey{}).(string)
	if !ok || userID == "" {
		return "", errors.New("user id missing from context")
	}
	return userID, nil
}

// assertClearedCookies verifies both auth cookies are expired by the response.
func assertClearedCookies(t *testing.T, res *httptest.ResponseRecorder) {
	t.Helper()

	cleared := map[string]bool{}
	for _, cookie := range res.Result().Cookies() {
		if cookie.MaxAge < 0 {
			cleared[cookie.Name] = true
		}
	}
	for _, name := range []string{csrfCookieName, sessionCookieName} {
		if !cleared[name] {
			t.Fatalf("cookie %q was not cleared (Set-Cookie: %q)", name, res.Header().Get("Set-Cookie"))
		}
	}
}

// TestIntegrationLogoutWithoutSessionStillClearsCookies proves the browser
// cookies are cleared even when the server cannot resolve the session, so a
// logout on a half-expired session cannot leave a live cookie behind.
func TestIntegrationLogoutWithoutSessionStillClearsCookies(t *testing.T) {
	ctx := t.Context()
	store := newSessionRedisStore(ctx, t)

	s := &Server{
		auth:          newAcceptingAuth(t),
		crypto:        newTestCrypto(t),
		rdb:           store,
		hostConfig:    &HostConfig{CookieDomain: "example.com", Secure: true, SameSite: http.SameSiteStrictMode},
		validationCfg: &ValidationConfig{SessionExpiry: time.Hour, CSRFExpiry: time.Hour, CSRFHMACSecret: testCSRFSecret},
	}

	res := httptest.NewRecorder()
	s.handleLogout(res, httptest.NewRequest(http.MethodPost, "/auth/logout", http.NoBody))

	if res.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusUnauthorized)
	}
	assertClearedCookies(t, res)
}

// TestIntegrationGetCSRFTokenReplacesStaleCookie verifies a stale csrf cookie
// is replaced and the fresh value is echoed, while a still-valid cookie is
// reused so concurrent tabs share one token.
func TestIntegrationGetCSRFTokenReplacesStaleCookie(t *testing.T) {
	ctx := t.Context()
	store := newSessionRedisStore(ctx, t)

	s := &Server{
		auth:          newAcceptingAuth(t),
		crypto:        newTestCrypto(t),
		rdb:           store,
		hostConfig:    &HostConfig{CookieDomain: "example.com", Secure: true, SameSite: http.SameSiteStrictMode},
		validationCfg: &ValidationConfig{SessionExpiry: time.Hour, CSRFExpiry: time.Hour, CSRFHMACSecret: testCSRFSecret},
	}
	session := mintSession(ctx, t, store, s)

	withSession := func() *http.Request {
		req := httptest.NewRequest(http.MethodGet, "/auth/csrf", http.NoBody)
		req = req.WithContext(context.WithValue(req.Context(), sessionKey{}, session))
		req.AddCookie(secureTestCookie(sessionCookieName, session))
		return req
	}

	t.Run("valid cookie is reused", func(t *testing.T) {
		valid, err := generateCSRFToken(session, testCSRFSecret)
		if err != nil {
			t.Fatalf("generate csrf token: %v", err)
		}
		req := withSession()
		req.AddCookie(secureTestCookie(csrfCookieName, valid))

		res := httptest.NewRecorder()
		s.handleGetCSRFToken(res, req)

		if res.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", res.Code, http.StatusOK)
		}
		if got := res.Body.String(); !strings.Contains(got, valid) {
			t.Fatalf("body = %q, want it to echo the presented token", got)
		}
		if got := res.Header().Get("Set-Cookie"); got != "" {
			t.Fatalf("expected no new csrf cookie, got %q", got)
		}
	})

	t.Run("expired cookie is replaced", func(t *testing.T) {
		staleStamp := strconv.FormatInt(time.Now().Add(-2*time.Hour).Unix(), 10)
		staleHMAC, err := generateHMAC(session+staleStamp, testCSRFSecret)
		if err != nil {
			t.Fatalf("generate hmac: %v", err)
		}
		req := withSession()
		req.AddCookie(secureTestCookie(csrfCookieName, staleHMAC+delimiter+staleStamp))

		res := httptest.NewRecorder()
		s.handleGetCSRFToken(res, req)

		if res.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", res.Code, http.StatusOK)
		}
		if got := res.Header().Get("Set-Cookie"); !strings.Contains(got, csrfCookieName+"=") {
			t.Fatalf("expected a replacement csrf cookie, got %q", got)
		}
		if strings.Contains(res.Body.String(), staleHMAC) {
			t.Fatalf("stale token was echoed back: %q", res.Body.String())
		}
		// The replacement must actually verify against this session.
		replacement := res.Result().Cookies()[0].Value
		if err := verifyCSRFToken(replacement, session, testCSRFSecret, time.Hour); err != nil {
			t.Fatalf("replacement token does not verify: %v", err)
		}
	})

	t.Run("foreign cookie is replaced", func(t *testing.T) {
		foreign, err := generateCSRFToken("another-session", testCSRFSecret)
		if err != nil {
			t.Fatalf("generate csrf token: %v", err)
		}
		req := withSession()
		req.AddCookie(secureTestCookie(csrfCookieName, foreign))

		res := httptest.NewRecorder()
		s.handleGetCSRFToken(res, req)

		if res.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", res.Code, http.StatusOK)
		}
		if strings.Contains(res.Body.String(), foreign) {
			t.Fatalf("token minted for another session was echoed: %q", res.Body.String())
		}
	})
}

// TestIntegrationVerifySessionMiddlewareRejectsRevokedToken proves a token the
// auth service refuses is rejected before Redis is consulted, so a revoked
// token cannot be revived by a lingering session key.
func TestIntegrationVerifySessionMiddlewareRejectsRevokedToken(t *testing.T) {
	ctx := t.Context()
	store := newSessionRedisStore(ctx, t)

	authService := authmock.NewMockIAuth(gomock.NewController(t))
	authService.EXPECT().
		ValidateToken(gomock.Any(), gomock.Any()).
		Return(nil, nil, status.Error(codes.Unauthenticated, "token revoked")).
		Times(1)

	s := &Server{auth: authService, crypto: newTestCrypto(t), rdb: store}
	session := mintSession(ctx, t, store, s)

	nextCalled := false
	handler := s.withVerifySessionMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/notifications", http.NoBody)
	req.AddCookie(secureTestCookie(sessionCookieName, session))

	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)

	if res.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusUnauthorized)
	}
	if got := res.Body.String(); got != "invalid token\n" {
		t.Fatalf("body = %q, want %q", got, "invalid token\n")
	}
	if nextCalled {
		t.Fatal("next handler ran for a revoked token")
	}
}
