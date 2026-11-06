//nolint:testpackage // Verifies logout failure through the real Redis client.
package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIntegrationLogoutReportsUnavailableSessionStore(t *testing.T) {
	store := newSessionRedisStore(t.Context(), t)
	require.NoError(t, store.Close())
	request := httptest.NewRequest(http.MethodPost, "/logout", http.NoBody)
	request = request.WithContext(context.WithValue(request.Context(), sessionKey{}, "session-token"))
	response := httptest.NewRecorder()
	(&Server{rdb: store, hostConfig: &HostConfig{Secure: true, SameSite: http.SameSiteStrictMode}}).handleLogout(response, request)
	require.Equal(t, http.StatusInternalServerError, response.Code)
	require.Equal(t, "failed to delete session\n", response.Body.String())
	cookies := response.Result().Cookies()
	require.Len(t, cookies, 2)
	require.Equal(t, csrfCookieName, cookies[0].Name)
	require.Equal(t, sessionCookieName, cookies[1].Name)
	for _, cookie := range cookies {
		require.Equal(t, -1, cookie.MaxAge)
		require.Empty(t, cookie.Value)
		require.True(t, cookie.Secure)
	}
}
