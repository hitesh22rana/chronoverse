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

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	workflowsmodel "github.com/hitesh22rana/chronoverse/internal/model/workflows"
	"github.com/hitesh22rana/chronoverse/internal/pkg/idempotency"
	jobspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"
)

const (
	serverShutdownTimeout = 10 * time.Second
	csrfCookieName        = "csrf"
	sessionCookieName     = "session"
	csrfHeaderName        = "X-CSRF-Token"
	idempotencyKeyHeader  = "Idempotency-Key"
	logStreamStdout       = "stdout"
	workflowKindHeartbeat = "HEARTBEAT"
	workflowKindContainer = "CONTAINER"
)

var (
	validKinds = []string{
		workflowKindHeartbeat,
		workflowKindContainer,
	}
	validBuildStatuses = []string{
		workflowsmodel.WorkflowBuildStatusQueued.ToString(),
		workflowsmodel.WorkflowBuildStatusStarted.ToString(),
		workflowsmodel.WorkflowBuildStatusCompleted.ToString(),
		workflowsmodel.WorkflowBuildStatusFailed.ToString(),
		workflowsmodel.WorkflowBuildStatusCanceled.ToString(),
	}
	validJobStatuses = []string{
		jobsmodel.JobStatusPending.ToString(),
		jobsmodel.JobStatusQueued.ToString(),
		jobsmodel.JobStatusRunning.ToString(),
		jobsmodel.JobStatusCompleted.ToString(),
		jobsmodel.JobStatusFailed.ToString(),
		jobsmodel.JobStatusCanceled.ToString(),
	}
	validJobTriggers = []string{
		jobsmodel.JobTriggerAutomatic.ToString(),
		jobsmodel.JobTriggerManual.ToString(),
	}
	terminalJobStatuses = []string{
		jobsmodel.JobStatusCompleted.ToString(),
		jobsmodel.JobStatusFailed.ToString(),
		jobsmodel.JobStatusCanceled.ToString(),
	}
)

// sessionKey is the key used to store the session in the context.
type sessionKey struct{}

// userIDKey is the key used to store the user ID in the context.
type userIDKey struct{}

func idempotencyKeyFromHeader(r *http.Request) (string, bool) {
	key := r.Header.Get(idempotencyKeyHeader)
	return key, key != ""
}

// decodeJSONRequest rejects non-JSON bodies before parsing. Requiring
// application/json forces a CORS preflight on cross-site browser requests,
// which the allowlist denies, blocking login CSRF via simple text/plain forms.
func decodeJSONRequest(r *http.Request, destination any) error {
	contentType := strings.ToLower(strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0]))
	if contentType != "application/json" {
		return status.Error(codes.InvalidArgument, "content-type must be application/json")
	}
	return idempotency.DecodeUniqueJSON(r.Body, destination)
}

// sessionFromContext returns the session from the context.
func sessionFromContext(ctx context.Context) (string, error) {
	session, ok := ctx.Value(sessionKey{}).(string)
	if !ok {
		return "", status.Error(codes.FailedPrecondition, "session not found in context")
	}

	return session, nil
}

// setCookie sets a host-only cookie. Domain is omitted so subdomains
// never receive session material. The csrf cookie stays readable so
// browser JS can echo it in X-CSRF-Token; session stays HttpOnly.
func setCookie(w http.ResponseWriter, name, value, _ string, secure bool, httpOnly bool, expires time.Duration, sameSite http.SameSite) {
	cookie := &http.Cookie{ //nolint:gosec // Secure is configurable so local HTTP development remains supported.
		Name:     name,
		Value:    value,
		Path:     "/",
		HttpOnly: httpOnly,
		Secure:   secure,
		MaxAge:   int(expires.Seconds()),
		SameSite: sameSite,
	}
	if expires < 0 {
		cookie.MaxAge = -1
		cookie.Expires = time.Unix(0, 0).UTC()
	}

	// Set the cookie in the response
	http.SetCookie(w, cookie)
}

// grpcToHTTPStatus maps gRPC codes to HTTP statuses. codes.OK is absent on
// purpose: like the switch it replaces, OK and unmapped codes write nothing.
var grpcToHTTPStatus = map[codes.Code]int{
	codes.Unauthenticated:    http.StatusUnauthorized,
	codes.PermissionDenied:   http.StatusForbidden,
	codes.NotFound:           http.StatusNotFound,
	codes.AlreadyExists:      http.StatusConflict,
	codes.Aborted:            http.StatusConflict,
	codes.InvalidArgument:    http.StatusBadRequest,
	codes.Unimplemented:      http.StatusNotImplemented,
	codes.Unavailable:        http.StatusServiceUnavailable,
	codes.FailedPrecondition: http.StatusPreconditionFailed,
	codes.ResourceExhausted:  http.StatusTooManyRequests,
	codes.Canceled:           http.StatusRequestTimeout,
	codes.DeadlineExceeded:   http.StatusGatewayTimeout,
	codes.Internal:           http.StatusInternalServerError,
	codes.DataLoss:           http.StatusInternalServerError,
	codes.OutOfRange:         http.StatusInternalServerError,
	codes.Unknown:            http.StatusInternalServerError,
}

func handleError(w http.ResponseWriter, err error, message ...string) {
	msg := err.Error()
	if len(message) > 0 {
		msg = strings.Join(message, " ")
	}

	code := status.Code(err)
	if code == codes.OK {
		return
	}
	httpStatus, ok := grpcToHTTPStatus[code]
	if !ok {
		return
	}
	http.Error(w, msg, httpStatus)
}

// gzipResponseWriter combines gzip compression with status code capture.
type gzipResponseWriter struct {
	http.ResponseWriter
	gzipWriter *gzip.Writer
	status     int
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

// isValidValue reports whether value belongs to the valid set.
func isValidValue(value string, valid []string) bool {
	return slices.Contains(valid, value)
}

// isValidKind checks if the given kind is valid.
func isValidKind(kind string) bool {
	return isValidValue(kind, validKinds)
}

// isValidBuildStatus checks if the given build status is valid.
func isValidBuildStatus(buildStatus string) bool {
	return isValidValue(buildStatus, validBuildStatuses)
}

// isValidJobStatus checks if the given job status is valid.
func isValidJobStatus(status string) bool {
	return isValidValue(status, validJobStatuses)
}

// isValidJobTrigger checks if the given job trigger is valid.
func isValidJobTrigger(trigger string) bool {
	return isValidValue(trigger, validJobTriggers)
}

// getJobLogsStreamType returns the joblogs stream type.
func getJobLogsStreamType(stream string) (jobspb.LogStream, error) {
	switch stream {
	case logStreamStdout:
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
	return isValidValue(status, terminalJobStatuses)
}
