//nolint:testpackage // Exercises private download handlers and writers.
package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	jobspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"
)

type downloadFailureClient struct {
	jobspb.JobsServiceClient
	jobError   error
	jobStatus  string
	fetchCalls int
	firstPage  *jobspb.GetJobLogsResponse
	lastPage   *jobspb.GetJobLogsResponse
	cursors    []string
	onFetch    func(int)
}

func (c *downloadFailureClient) GetJob(context.Context, *jobspb.GetJobRequest, ...grpc.CallOption) (*jobspb.GetJobResponse, error) {
	return &jobspb.GetJobResponse{Status: c.jobStatus}, c.jobError
}

func (c *downloadFailureClient) GetJobLogs(
	_ context.Context,
	req *jobspb.GetJobLogsRequest,
	_ ...grpc.CallOption,
) (
	*jobspb.GetJobLogsResponse,
	error,
) {
	c.fetchCalls++
	c.cursors = append(c.cursors, req.GetCursor())
	if c.onFetch != nil {
		c.onFetch(c.fetchCalls)
	}
	if c.fetchCalls == 1 && c.firstPage != nil {
		return c.firstPage, nil
	}
	if c.fetchCalls == 2 && c.lastPage != nil {
		return c.lastPage, nil
	}
	return nil, status.Error(codes.Unavailable, "private backend failure")
}

func TestDownloadStopsBeforeFetchingUnreadyJobs(t *testing.T) {
	for _, test := range []struct {
		name, state string
		err         error
		status      int
		body        string
	}{
		{"backend error", "", status.Error(codes.Unavailable, "backend unavailable"), http.StatusServiceUnavailable, "failed to get job\n"},
		{"running job", "RUNNING", nil, http.StatusBadRequest, "job is not yet completed\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := &downloadFailureClient{jobError: test.err, jobStatus: test.state}
			s := &Server{jobsClient: client, logger: zap.NewNop()}
			response := httptest.NewRecorder()
			s.handleDownloadJobLogs(response, newJobLogsHandlerRequest("/download"))
			require.Equal(t, test.status, response.Code)
			require.Equal(t, test.body, response.Body.String())
			require.Zero(t, client.fetchCalls)
		})
	}
}

func TestDownloadAuthorizesPageOneBeforeSuccess(t *testing.T) {
	client := &downloadFailureClient{jobStatus: "COMPLETED"}
	response := httptest.NewRecorder()
	(&Server{jobsClient: client}).handleDownloadJobLogs(response, newJobLogsHandlerRequest("/download"))
	require.Equal(t, http.StatusServiceUnavailable, response.Code)
	require.Equal(t, "failed to fetch logs\n", response.Body.String())
	require.Empty(t, response.Header().Get("Content-Disposition"))
	require.False(t, response.Flushed)
	require.Equal(t, []string{""}, client.cursors)
}

func TestDownloadLaterPageFailureHasNoCompletionAndKeepsTrace(t *testing.T) {
	for _, test := range []struct {
		name         string
		repeatCursor bool
		disconnect   bool
		failedWrite  int
		wantRecord   string
		wantLog      string
		wantCause    string
	}{
		{
			name: "fetch failure", wantRecord: "{\"type\":\"error\",\"message\":\"stream failed\"}\n",
			wantLog: "failed to fetch logs", wantCause: "private backend failure",
		},
		{
			name: "nonadvancing cursor", repeatCursor: true,
			wantRecord: "{\"type\":\"error\",\"message\":\"stream failed\"}\n",
			wantLog:    "failed to fetch logs", wantCause: "log cursor did not advance",
		},
		{
			name: "request canceled while fetching", disconnect: true,
			wantLog: "failed to fetch logs", wantCause: "private backend failure",
		},
		{
			name: "error record write fails", failedWrite: 2,
			wantLog: "failed to write log download error", wantCause: "closed pipe",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := &downloadFailureClient{jobStatus: "COMPLETED", firstPage: &jobspb.GetJobLogsResponse{
				Logs: []*jobspb.Log{{Message: "line"}}, Cursor: "next",
			}}
			if test.repeatCursor {
				client.lastPage = &jobspb.GetJobLogsResponse{Cursor: "next"}
			}
			core, entries := observer.New(zap.ErrorLevel)
			span := trace.NewSpanContext(trace.SpanContextConfig{TraceID: trace.TraceID{1}, SpanID: trace.SpanID{2}})
			request := newJobLogsHandlerRequest("/download")
			ctx, cancel := context.WithCancel(trace.ContextWithSpanContext(request.Context(), span))
			defer cancel()
			if test.disconnect {
				client.onFetch = func(page int) {
					if page == 2 {
						cancel()
					}
				}
			}

			response := &failedLogResponse{ResponseRecorder: httptest.NewRecorder(), at: test.failedWrite}
			(&Server{jobsClient: client, logger: zap.New(core)}).handleDownloadJobLogs(response, request.WithContext(ctx))
			require.Equal(t, http.StatusOK, response.Code)
			require.Equal(t, []string{"", "next"}, client.cursors)
			require.True(t, response.Flushed)
			_, terminal, ok := strings.Cut(response.Body.String(), "\n")
			require.True(t, ok)
			require.Contains(t, response.Body.String(), `"message_raw":"line"`)
			require.Equal(t, test.wantRecord, terminal)

			logs := entries.FilterMessage(test.wantLog).All()
			require.Len(t, logs, 1)
			require.Equal(t, span.TraceID().String(), logs[0].ContextMap()["trace_id"])
			require.Contains(t, logs[0].ContextMap()["error"], test.wantCause)
		})
	}
}

func TestDownloadCompletesAfterAllPages(t *testing.T) {
	client := &downloadFailureClient{
		jobStatus: "COMPLETED",
		firstPage: &jobspb.GetJobLogsResponse{Logs: []*jobspb.Log{{Message: "first"}}, Cursor: "next"},
		lastPage:  &jobspb.GetJobLogsResponse{Logs: []*jobspb.Log{{Message: "last"}}},
	}
	response := httptest.NewRecorder()
	(&Server{jobsClient: client}).handleDownloadJobLogs(response, newJobLogsHandlerRequest("/download"))
	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, []string{"", "next"}, client.cursors)
	lines := strings.Split(strings.TrimSuffix(response.Body.String(), "\n"), "\n")
	require.Len(t, lines, 3)
	require.Contains(t, lines[0], `"message_raw":"first"`)
	require.Contains(t, lines[1], `"message_raw":"last"`)
	require.JSONEq(t, `{"type":"complete"}`, lines[2])
}

func TestDownloadHeadAuthorizesOnlyFirstPage(t *testing.T) {
	client := &downloadFailureClient{jobStatus: "COMPLETED", firstPage: &jobspb.GetJobLogsResponse{Cursor: "next"}}
	request := newJobLogsHandlerRequest("/download")
	request.Method = http.MethodHead
	response := httptest.NewRecorder()
	(&Server{jobsClient: client}).handleDownloadJobLogs(response, request)
	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, "application/x-ndjson", response.Header().Get("Content-Type"))
	require.Empty(t, response.Body.String())
	require.Equal(t, []string{""}, client.cursors)
}
