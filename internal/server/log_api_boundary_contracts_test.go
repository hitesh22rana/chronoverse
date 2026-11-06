//nolint:testpackage // Exercises HTTP validation and streaming writer failures.
package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
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

func TestDownloadRejectsUnsupportedFormatAndStreamBeforeRPC(t *testing.T) {
	t.Parallel()
	for _, query := range []string{"?format=xml", "?stream=invalid"} {
		response := httptest.NewRecorder()
		(&Server{}).handleDownloadJobLogs(response, newJobLogsHandlerRequest("/download"+query))
		require.Equal(t, http.StatusBadRequest, response.Code)
	}
}

type logBoundaryClient struct {
	jobspb.JobsServiceClient
	err     error
	status  string
	fetches int
	logs    []*jobspb.Log
}

func (c *logBoundaryClient) GetJob(context.Context, *jobspb.GetJobRequest, ...grpc.CallOption) (*jobspb.GetJobResponse, error) {
	return &jobspb.GetJobResponse{Status: c.status}, c.err
}

func (c *logBoundaryClient) GetJobLogs(context.Context, *jobspb.GetJobLogsRequest, ...grpc.CallOption) (*jobspb.GetJobLogsResponse, error) {
	c.fetches++
	if c.fetches > 1 {
		return nil, status.Error(codes.Internal, "unexpected next page")
	}
	return &jobspb.GetJobLogsResponse{Logs: c.logs, Cursor: "next"}, c.err
}

func (c *logBoundaryClient) ScheduleJob(context.Context, *jobspb.ScheduleJobRequest, ...grpc.CallOption) (*jobspb.ScheduleJobResponse, error) {
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
	require.Empty(t, response.Header().Get("Cache-Control"))
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

func TestDownloadRequiresFlushingAndStopsOnHeaderOrRecordWriteFailure(t *testing.T) {
	t.Parallel()
	request := newJobLogsHandlerRequest("/download?format=json")
	client := &logBoundaryClient{status: "COMPLETED"}
	response := httptest.NewRecorder()
	(&Server{jobsClient: client}).handleDownloadJobLogs(nonStreamingLogResponse{response}, request)
	require.Equal(t, http.StatusInternalServerError, response.Code)
	require.Equal(t, 0, client.fetches)
	for _, at := range []int{1, 2} {
		writer := &failedLogResponse{ResponseRecorder: httptest.NewRecorder(), at: at}
		(&Server{jobsClient: client}).handleDownloadJobLogs(writer, request)
		require.Equal(t, 0, client.fetches)
		require.Contains(t, writer.Body.String(), "failed to write logs")
	}
	for _, tc := range []struct {
		target string
		failAt int
	}{{"/download", 1}, {"/download?format=json", 3}} {
		client := &logBoundaryClient{status: "COMPLETED", logs: []*jobspb.Log{{Message: "line"}}}
		writer := &failedLogResponse{ResponseRecorder: httptest.NewRecorder(), at: tc.failAt}
		core, entries := observer.New(zap.ErrorLevel)
		(&Server{jobsClient: client, logger: zap.New(core)}).handleDownloadJobLogs(writer, newJobLogsHandlerRequest(tc.target))
		require.Equal(t, 1, client.fetches)
		require.True(t, writer.Flushed)
		if tc.failAt == 3 {
			require.True(t, json.Valid(writer.Body.Bytes()), writer.Body.String())
			require.JSONEq(t, `{"id":"550e8400-e29b-41d4-a716-446655440000","workflow_id":"workflow_id","filters":{"q":"","stream":"all"},"logs":[],"error":{"message":"stream failed"}}`, writer.Body.String())
		} else {
			require.Contains(t, writer.Body.String(), "--- ERROR: stream failed ---")
		}
		require.Len(t, entries.All(), 1)
		require.Equal(t, "failed to write log", entries.All()[0].Message)
		require.Equal(t, io.ErrClosedPipe.Error(), entries.All()[0].ContextMap()["error"])
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
