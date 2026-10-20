//nolint:testpackage // Verifies constructor wiring and its private HTTP handler.
package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.uber.org/mock/gomock"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	authmock "github.com/hitesh22rana/chronoverse/internal/pkg/auth/mock"
	"github.com/hitesh22rana/chronoverse/internal/pkg/crypto"
	loggerpkg "github.com/hitesh22rana/chronoverse/internal/pkg/logger"
	"github.com/hitesh22rana/chronoverse/internal/pkg/redis"
)

func TestNewConfiguresHTTPServer(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, hostURL, mode string
		secure              bool
		sameSite            http.SameSite
	}{
		{"strict HTTPS", "https://app.example.test:8443", "STRICT", true, http.SameSiteStrictMode},
		{"lax HTTP", "http://app.example.test", "LAX", false, http.SameSiteLaxMode},
		{"none", "https://app.example.test", "NONE", true, http.SameSiteNoneMode},
		{"unspecified", "https://app.example.test", "", true, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			validation := &ValidationConfig{RequestBodyLimit: 512, CSRFHMACSecret: "constructor-test"}
			cfg := &Config{
				Host: "127.0.0.1", Port: 8081, ReadTimeout: 2 * time.Second, ReadHeaderTimeout: 3 * time.Second, IdleTimeout: 4 * time.Second,
				HostURL: tc.hostURL, SameSiteMode: tc.mode, CookieDomain: "example.test", ValidationConfig: validation,
				AllowedOrigins: []string{"https://dashboard.example.test", "https://dashboard.example.test"},
			}
			auth := authmock.NewMockIAuth(gomock.NewController(t))
			crypt := &crypto.Crypto{}
			store := &redis.Store{}
			users := &sessionUsersClient{}
			workflows := &recordingWorkflowsClient{}
			jobs := &fakeJobsServiceClient{}
			notifications := &fakeNotificationsServiceClient{}
			analytics := &fakeAnalyticsServiceClient{}
			srv := New(t.Context(), cfg, auth, crypt, store, users, workflows, jobs, notifications, analytics)
			require.Same(t, auth, srv.auth)
			require.Same(t, crypt, srv.crypto)
			require.Same(t, store, srv.rdb)
			require.Same(t, users, srv.usersClient)
			require.Same(t, workflows, srv.workflowsClient)
			require.Same(t, jobs, srv.jobsClient)
			require.Same(t, notifications, srv.notificationsClient)
			require.Same(t, analytics, srv.analyticsClient)
			require.Equal(t, "127.0.0.1:8081", srv.httpServer.Addr)
			require.Equal(t, 2*time.Second, srv.httpServer.ReadTimeout)
			require.Equal(t, 3*time.Second, srv.httpServer.ReadHeaderTimeout)
			require.Equal(t, 4*time.Second, srv.httpServer.IdleTimeout)
			require.Same(t, validation, srv.validationCfg)
			require.Equal(t, &HostConfig{URL: tc.hostURL, Host: "app.example.test", Secure: tc.secure, SameSite: tc.sameSite, CookieDomain: "example.test"}, srv.hostConfig)
			require.Equal(t, map[string]struct{}{"https://dashboard.example.test": {}}, srv.allowedOrigins)
			require.NotNil(t, srv.logger)

			request := httptest.NewRequest(http.MethodOptions, "/users", http.NoBody)
			request.Header.Set("Origin", "https://dashboard.example.test")
			response := httptest.NewRecorder()
			srv.httpServer.Handler.ServeHTTP(response, request)
			require.Equal(t, http.StatusNoContent, response.Code)
			require.Equal(t, "https://dashboard.example.test", response.Header().Get("Access-Control-Allow-Origin"))
			require.Equal(t, "nosniff", response.Header().Get("X-Content-Type-Options"))
			if tc.secure {
				require.NotEmpty(t, response.Header().Get("Strict-Transport-Security"))
			} else {
				require.Empty(t, response.Header().Get("Strict-Transport-Security"))
			}

			response = httptest.NewRecorder()
			srv.httpServer.Handler.ServeHTTP(response, httptest.NewRequest(http.MethodTrace, "/users", http.NoBody))
			require.Equal(t, http.StatusMethodNotAllowed, response.Code)
			require.Equal(t, "GET, HEAD, PUT", response.Header().Get("Allow"))
		})
	}
}

func TestNewRejectsMalformedHostURL(t *testing.T) {
	t.Parallel()
	provider := sdklog.NewLoggerProvider()
	t.Cleanup(func() { require.NoError(t, provider.Shutdown(context.Background())) })
	ctx, logger := loggerpkg.Init(t.Context(), "constructor-test", provider)
	core, logs := observer.New(zap.InfoLevel)
	// The context retains this logger; its test hook turns Fatal into a panic.
	*logger = *logger.WithOptions(zap.WrapCore(func(zapcore.Core) zapcore.Core { return core }), zap.WithFatalHook(zapcore.WriteThenPanic))
	require.PanicsWithValue(t, "failed to parse host URL", func() { New(ctx, &Config{HostURL: "://%"}, nil, nil, nil, nil, nil, nil, nil, nil) })
	entries := logs.FilterMessage("failed to parse host URL").All()
	require.Len(t, entries, 1)
	require.Contains(t, entries[0].ContextMap()["error"], "parse")
}
