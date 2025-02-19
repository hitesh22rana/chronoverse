package server

import (
	"context"
	"net/http"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	serverShutdownTimeout = 10 * time.Second
	csrfCookieName        = "csrf"
	sessionCookieName     = "session"
	csrfHeaderKey         = "X-CSRF-Token"
	sessionHeaderKey      = "X-Session-Token"
)

// sessionKey is the key used to store the session in the context.
type sessionKey struct{}

// sessionFromContext returns the session from the context.
func sessionFromContext(ctx context.Context) (string, error) {
	session, ok := ctx.Value(sessionKey{}).(string)
	if !ok {
		return "", status.Error(codes.FailedPrecondition, "session not found in context")
	}

	return session, nil
}

// setCookie sets a cookie in the response.
func setCookie(w http.ResponseWriter, name, value string, expires time.Duration) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		Secure:   false,
		Expires:  time.Now().Add(expires),
		SameSite: http.SameSiteLaxMode,
	})
}
