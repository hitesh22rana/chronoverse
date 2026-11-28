//nolint:testpackage // Exercises HTTP validation and streaming writer failures.
package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	jobspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"
)

func TestJobEndpointsRejectMissingRouteAndOwnerBeforeRPC(t *testing.T) {
	t.Parallel()
	for _, endpoint := range []struct {
		name    string
		handler func(*Server, http.ResponseWriter, *http.Request)
		job     bool
	}{
		{"job", (*Server).handleGetJob, true},
		{"logs", (*Server).handleGetJobLogs, true},
		{"download", (*Server).handleDownloadJobLogs, true},
		{"events", (*Server).handleJobEvents, true},
		{"schedule", (*Server).handleManualScheduleJob, false},
	} {
		for _, missing := range []string{"workflow", "job", "owner", "invalid owner", "empty owner"} {
			if missing == "job" && !endpoint.job {
				continue
			}
			t.Run(endpoint.name+"/"+missing, func(t *testing.T) {
				t.Parallel()
				request := newJobLogsHandlerRequest("/logs")
				request.Header.Set(idempotencyKeyHeader, "manual-command")
				want := http.StatusBadRequest
				switch missing {
				case "workflow":
					request.SetPathValue("workflow_id", "")
				case "job":
					request.SetPathValue("job_id", "")
				case "owner":
					request = request.WithContext(context.Background())
				case "invalid owner":
					request = request.WithContext(context.WithValue(request.Context(), userIDKey{}, 7))
				case "empty owner":
					request = request.WithContext(context.WithValue(request.Context(), userIDKey{}, ""))
				}
				response := httptest.NewRecorder()
				endpoint.handler(&Server{}, response, request)
				require.Equal(t, want, response.Code)
			})
		}
	}
}

func TestDownloadRejectsUnsupportedStreamBeforeRPC(t *testing.T) {
	t.Parallel()
	response := httptest.NewRecorder()
	(&Server{}).handleDownloadJobLogs(response, newJobLogsHandlerRequest("/download?stream=invalid"))
	require.Equal(t, http.StatusBadRequest, response.Code)
}

type logBoundaryClient struct {
	jobspb.JobsServiceClient
	err     error
	status  string
	fetches int
}

func (c *logBoundaryClient) GetJob(context.Context, *jobspb.GetJobRequest, ...grpc.CallOption) (*jobspb.GetJobResponse, error) {
	return &jobspb.GetJobResponse{Status: c.status}, c.err
}

func (c *logBoundaryClient) GetJobLogs(context.Context, *jobspb.GetJobLogsRequest, ...grpc.CallOption) (*jobspb.GetJobLogsResponse, error) {
	c.fetches++
	if c.fetches > 1 {
		return nil, status.Error(codes.Internal, "unexpected next page")
	}
	return &jobspb.GetJobLogsResponse{}, c.err
}

func (c *logBoundaryClient) ScheduleJob(
	context.Context,
	*jobspb.ScheduleJobRequest,
	...grpc.CallOption,
) (
	*jobspb.ScheduleJobResponse,
	error,
) {
	return nil, c.err
}

func TestJobReadAndManualScheduleMapBackendFailure(t *testing.T) {
	t.Parallel()
	for _, handler := range []func(*Server, http.ResponseWriter, *http.Request){(*Server).handleGetJob, (*Server).handleGetJobLogs, (*Server).handleManualScheduleJob} {
		request := newJobLogsHandlerRequest("/jobs")
		request.Header.Set(idempotencyKeyHeader, "command")
		response := httptest.NewRecorder()
		handler(&Server{jobsClient: &logBoundaryClient{err: status.Error(codes.Unavailable, "private backend")}}, response, request)
		require.Equal(t, http.StatusServiceUnavailable, response.Code)
		require.NotContains(t, response.Body.String(), "private backend")
	}
	response := httptest.NewRecorder()
	(&Server{jobsClient: &logBoundaryClient{status: "RUNNING"}}).handleGetJob(response, newJobLogsHandlerRequest("/jobs"))
	require.Equal(t, http.StatusOK, response.Code)
	require.Empty(t, response.Header().Get("Vary"))
}

type failedLogResponse struct {
	*httptest.ResponseRecorder
	at, calls int
}

func (w *failedLogResponse) Write(data []byte) (int, error) {
	w.calls++
	if w.calls == w.at {
		return 0, io.ErrClosedPipe
	}
	return w.ResponseRecorder.Write(data)
}

type nonStreamingLogResponse struct{ http.ResponseWriter }

func TestDownloadStopsOnRecordWriteFailure(t *testing.T) {
	t.Parallel()
	request := newJobLogsHandlerRequest("/download")
	client := &logBoundaryClient{status: "COMPLETED"}
	response := httptest.NewRecorder()
	(&Server{jobsClient: client}).handleDownloadJobLogs(nonStreamingLogResponse{response}, request)
	require.Equal(t, http.StatusInternalServerError, response.Code)
	require.Zero(t, client.fetches)
	for _, test := range []struct {
		name   string
		at     int
		log    string
		cursor string
	}{
		{name: "log", at: 1, log: "failed to write log", cursor: "next"},
		{name: "completion", at: 2, log: "failed to write log completion"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			writer := &failedLogResponse{ResponseRecorder: httptest.NewRecorder(), at: test.at}
			core, entries := observer.New(zap.ErrorLevel)
			client := &downloadFailureClient{jobStatus: "COMPLETED", firstPage: &jobspb.GetJobLogsResponse{
				Logs: []*jobspb.Log{{Message: "line"}}, Cursor: test.cursor,
			}}
			(&Server{jobsClient: client, logger: zap.New(core)}).handleDownloadJobLogs(writer, request)
			require.Equal(t, []string{""}, client.cursors)
			require.NotContains(t, writer.Body.String(), `"type":"complete"`)
			require.Equal(t, test.at-1, strings.Count(writer.Body.String(), "\n"))
			require.Len(t, entries.All(), 1)
			require.Equal(t, test.log, entries.All()[0].Message)
			require.Equal(t, io.ErrClosedPipe.Error(), entries.All()[0].ContextMap()["error"])
		})
	}
}

func TestLogoutWithoutSessionExpiresCookiesAndRejectsRequest(t *testing.T) {
	t.Parallel()
	response := httptest.NewRecorder()
	(&Server{hostConfig: &HostConfig{Secure: true, SameSite: http.SameSiteStrictMode}}).handleLogout(response, httptest.NewRequest(http.MethodPost, "/logout", http.NoBody))
	require.Equal(t, http.StatusUnauthorized, response.Code)
	cookies := response.Result().Cookies()
	require.Len(t, cookies, 2)
	require.Equal(t, csrfCookieName, cookies[0].Name)
	require.Equal(t, sessionCookieName, cookies[1].Name)
	for _, cookie := range cookies {
		require.Equal(t, -1, cookie.MaxAge)
		require.True(t, cookie.Secure)
		require.Empty(t, cookie.Value)
	}
	require.False(t, cookies[0].HttpOnly)
	require.True(t, cookies[1].HttpOnly)
}

func TestRequestLoggingKeepsTraceAndResponseStatus(t *testing.T) {
	t.Parallel()
	core, entries := observer.New(zap.InfoLevel)
	span := trace.NewSpanContext(trace.SpanContextConfig{TraceID: trace.TraceID{1}, SpanID: trace.SpanID{2}})
	request := httptest.NewRequest(http.MethodGet, "/jobs?token=private", http.NoBody)
	request = request.WithContext(trace.ContextWithSpanContext(request.Context(), span))
	response := httptest.NewRecorder()
	(&Server{logger: zap.New(core)}).withRequestLoggingMiddleware(http.HandlerFunc(func(w http.ResponseWriter,
		_ *http.Request,
	) {
		w.WriteHeader(http.StatusAccepted)
	})).ServeHTTP(response,
		request)
	require.Equal(t, http.StatusAccepted, response.Code)
	require.Len(t, entries.All(), 1)
	fields := entries.All()[0].ContextMap()
	require.Equal(t, span.TraceID().String(), fields["trace_id"])
	require.Equal(t, span.SpanID().String(), fields["span_id"])
	require.EqualValues(t, http.StatusAccepted, fields["status"])
	require.Equal(t, "/jobs", fields["path"])
	require.NotContains(t, entries.All()[0].Message, "private")
}

func TestEventStreamRejectsWritersWithoutFlushing(t *testing.T) {
	t.Parallel()
	response := httptest.NewRecorder()
	(&Server{}).handleJobEvents(nonStreamingLogResponse{response}, newJobLogsHandlerRequest("/events"))
	require.Equal(t, http.StatusInternalServerError, response.Code)
	require.Equal(t, "Streaming unsupported\n", response.Body.String())
}

func TestRequestLoggingClassifiesClientAndServerFailures(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		status int
		level  zapcore.Level
	}{{http.StatusBadRequest, zap.WarnLevel}, {http.StatusServiceUnavailable, zap.ErrorLevel}} {
		core, entries := observer.New(zap.InfoLevel)
		response := httptest.NewRecorder()
		handler := (&Server{logger: zap.New(core)}).withRequestLoggingMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(tc.status)
		}))
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/jobs", http.NoBody))
		require.Equal(t, tc.status, response.Code)
		require.Len(t, entries.All(), 1)
		require.Equal(t, tc.level, entries.All()[0].Level)
		require.EqualValues(t, tc.status, entries.All()[0].ContextMap()["status"])
	}
}
