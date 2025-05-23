package server

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	serverShutdownTimeout = 10 * time.Second
	csrfCookieName        = "csrf"
	sessionCookieName     = "session"
)

var (
	validKinds = []string{
		"HEARTBEAT",
		"CONTAINER",
	}
	validBuildStatuses = []string{
		"QUEUED",
		"STARTED",
		"COMPLETED",
		"FAILED",
		"CANCELED",
	}
)

// sessionKey is the key used to store the session in the context.
type sessionKey struct{}

// userIDKey is the key used to store the user ID in the context.
type userIDKey struct{}

// sessionFromContext returns the session from the context.
func sessionFromContext(ctx context.Context) (string, error) {
	session, ok := ctx.Value(sessionKey{}).(string)
	if !ok {
		return "", status.Error(codes.FailedPrecondition, "session not found in context")
	}

	return session, nil
}

// setCookie sets a cookie in the response.
func setCookie(w http.ResponseWriter, name, value, host string, secure bool, expires time.Duration) {
	cookie := &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		Expires:  time.Now().Add(expires),
		SameSite: http.SameSiteStrictMode,
	}

	// Set domain only for non-localhost
	if host != "localhost" {
		cookie.Domain = host
	}

	// Set the cookie in the response
	http.SetCookie(w, cookie)
}

//nolint:gocyclo // handleErrors is a helper function to handle gRPC errors.
func handleError(w http.ResponseWriter, err error, message ...string) {
	msg := err.Error()
	if len(message) > 0 {
		msg = strings.Join(message, " ")
	}

	switch status.Code(err) {
	case codes.OK:
		return
	case codes.Unauthenticated:
		http.Error(w, msg, http.StatusUnauthorized)
	case codes.PermissionDenied:
		http.Error(w, msg, http.StatusForbidden)
	case codes.NotFound:
		http.Error(w, msg, http.StatusNotFound)
	case codes.AlreadyExists:
		http.Error(w, msg, http.StatusConflict)
	case codes.InvalidArgument:
		http.Error(w, msg, http.StatusBadRequest)
	case codes.Unimplemented:
		http.Error(w, msg, http.StatusNotImplemented)
	case codes.Unavailable:
		http.Error(w, msg, http.StatusServiceUnavailable)
	case codes.FailedPrecondition:
		http.Error(w, msg, http.StatusPreconditionFailed)
	case codes.ResourceExhausted:
		http.Error(w, msg, http.StatusTooManyRequests)
	case codes.Canceled:
		http.Error(w, msg, http.StatusRequestTimeout)
	case codes.DeadlineExceeded:
		http.Error(w, msg, http.StatusGatewayTimeout)
	case codes.Internal:
		http.Error(w, msg, http.StatusInternalServerError)
	case codes.DataLoss:
		http.Error(w, msg, http.StatusInternalServerError)
	case codes.Aborted:
		http.Error(w, msg, http.StatusInternalServerError)
	case codes.OutOfRange:
		http.Error(w, msg, http.StatusInternalServerError)
	case codes.Unknown:
		http.Error(w, msg, http.StatusInternalServerError)
	}
}

// statusResponseWriter is a wrapper around http.ResponseWriter that captures the status code.
type statusResponseWriter struct {
	http.ResponseWriter
	status int
}

// WriteHeader captures the status code before calling the underlying WriteHeader.
func (w *statusResponseWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

// isValidKind checks if the given kind is valid.
func isValidKind(kind string) bool {
	return slices.Contains(validKinds, kind)
}

// isValidBuildStatus checks if the given build status is valid.
func isValidBuildStatus(buildStatus string) bool {
	return slices.Contains(validBuildStatuses, buildStatus)
}
