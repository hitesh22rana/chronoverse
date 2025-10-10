package server

import (
	"compress/gzip"
	"context"
	"net/http"
	"slices"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	jobspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"
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
	validJobStatuses = []string{
		"PENDING",
		"QUEUED",
		"RUNNING",
		"COMPLETED",
		"FAILED",
		"CANCELED",
	}
	terminalJobStatuses = []string{
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
func setCookie(w http.ResponseWriter, name, value, host string, secure bool, expires time.Duration, sameSite http.SameSite) {
	cookie := &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		MaxAge:   int(expires.Seconds()),
		SameSite: sameSite,
	}

	// Only set Domain for non-localhost and non-127.0.0.1
	if !strings.Contains(host, "localhost") && !strings.Contains(host, "127.0.0.1") {
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

// customResponseWriter wraps http.ResponseWriter to capture status code.
type customResponseWriter struct {
	http.ResponseWriter
	status int
}

func (w *customResponseWriter) WriteHeader(statusCode int) {
	w.status = statusCode
	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *customResponseWriter) Write(b []byte) (int, error) {
	return w.ResponseWriter.Write(b)
}

// gzipResponseWriter combines gzip compression with status code capture.
type gzipResponseWriter struct {
	http.ResponseWriter
	gzipWriter *gzip.Writer
	status     int
}

func (w *gzipResponseWriter) Header() http.Header {
	return w.ResponseWriter.Header()
}

func (w *gzipResponseWriter) WriteHeader(statusCode int) {
	w.status = statusCode
	w.Header().Set("Content-Encoding", "gzip")
	w.Header().Del("Content-Length") // Will change after compression
	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *gzipResponseWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.gzipWriter.Write(b)
}

// isValidKind checks if the given kind is valid.
func isValidKind(kind string) bool {
	return slices.Contains(validKinds, kind)
}

// isValidBuildStatus checks if the given build status is valid.
func isValidBuildStatus(buildStatus string) bool {
	return slices.Contains(validBuildStatuses, buildStatus)
}

// isValidJobStatus checks if the given job status is valid.
func isValidJobStatus(status string) bool {
	return slices.Contains(validJobStatuses, status)
}

// getJobLogsStreamType returns the joblogs stream type.
func getJobLogsStreamType(stream string) (jobspb.LogStream, error) {
	switch stream {
	case "stdout":
		return jobspb.LogStream_LOG_STREAM_STDOUT, nil
	case "stderr":
		return jobspb.LogStream_LOG_STREAM_STDERR, nil
	case "":
		return jobspb.LogStream_LOG_STREAM_ALL, nil
	default:
		return jobspb.LogStream_LOG_STREAM_UNSPECIFIED, status.Errorf(codes.InvalidArgument, "invalid log stream type")
	}
}

// isTerminalJobStatus checks if the given job status is terminal(will no longer change).
func isTerminalJobStatus(status string) bool {
	return slices.Contains(terminalJobStatuses, status)
}
