//nolint:testpackage // Exercises private download handlers and writers.
package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
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

type failingDownloadWriter struct {
	failAt, calls int
	err           error
}

func (w *failingDownloadWriter) Write(data []byte) (int, error) {
	w.calls++
	if w.calls == w.failAt {
		return 0, w.err
	}
	return len(data), nil
}

func TestDownloadWriterPropagatesClientWriteFailures(t *testing.T) {
	disconnected := errors.New("client disconnected")
	for _, failAt := range []int{1, 2} {
		writer := &failingDownloadWriter{failAt: failAt, err: disconnected}
		require.ErrorIs(t, writeJobLogsDownloadJSONHeader(writer, &jobLogsDownloadRequest{JobID: "job"}), disconnected)
		require.Equal(t, failAt, writer.calls)
	}
	for _, test := range []struct {
		name, format string
		first        bool
		failAt       int
	}{
		{"text", jobLogsDownloadFormatText, true, 1},
		{"jsonl", jobLogsDownloadFormatJSONL, true, 1},
		{"first JSON record", jobLogsDownloadFormatJSON, true, 1},
		{"JSON delimiter", jobLogsDownloadFormatJSON, false, 1},
		{"later JSON record", jobLogsDownloadFormatJSON, false, 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			writer := &failingDownloadWriter{failAt: test.failAt, err: disconnected}
			first := test.first
			require.ErrorIs(t, writeJobLogsDownloadLog(writer, test.format, &jobspb.Log{Message: "hello"}, &first), disconnected)
			require.Equal(t, test.failAt, writer.calls)
		})
	}
}

func TestDownloadErrorsHideBackendDetailsAndKeepTraceCorrelation(t *testing.T) {
	core, entries := observer.New(zap.ErrorLevel)
	s := &Server{logger: zap.New(core)}
	span := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: trace.TraceID{1}, SpanID: trace.SpanID{2},
	})
	ctx := trace.ContextWithSpanContext(context.Background(), span)
	backendError := errors.New("password=private backend address")
	for _, test := range []struct{ format, expected string }{
		{jobLogsDownloadFormatJSON, `],"error":{"message":"stream failed"}}`},
		{jobLogsDownloadFormatJSONL, "{\"message\":\"stream failed\"}\n"},
		{jobLogsDownloadFormatText, "\n--- ERROR: stream failed ---\n"},
	} {
		var response bytes.Buffer
		s.writeJobLogsDownloadError(ctx, &response, test.format, "failed to fetch logs", backendError)
		require.Equal(t, test.expected, response.String())
		require.NotContains(t, response.String(), backendError.Error())
		entry := entries.All()[entries.Len()-1]
		require.Equal(t, "failed to fetch logs", entry.Message)
		require.Equal(t, backendError.Error(), entry.ContextMap()["error"])
		require.Equal(t, span.TraceID().String(), entry.ContextMap()["trace_id"])
	}
	s.writeJobLogsDownloadError(context.Background(), io.Discard, jobLogsDownloadFormatText, "without span", backendError)
	require.NotContains(t, entries.All()[entries.Len()-1].ContextMap(), "trace_id")
}

type downloadFailureClient struct {
	jobspb.JobsServiceClient
	jobError   error
	jobStatus  string
	fetchCalls int
}

func (c *downloadFailureClient) GetJob(context.Context, *jobspb.GetJobRequest, ...grpc.CallOption) (*jobspb.GetJobResponse, error) {
	return &jobspb.GetJobResponse{Status: c.jobStatus}, c.jobError
}

func (c *downloadFailureClient) GetJobLogs(context.Context, *jobspb.GetJobLogsRequest, ...grpc.CallOption) (*jobspb.GetJobLogsResponse, error) {
	c.fetchCalls++
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

func TestDownloadReportsFetchFailureWithoutClosingAsSuccess(t *testing.T) {
	client := &downloadFailureClient{jobStatus: "COMPLETED"}
	s := &Server{jobsClient: client, logger: zap.NewNop()}
	response := httptest.NewRecorder()
	request := newJobLogsHandlerRequest("/download?format=json")
	s.handleDownloadJobLogs(response, request)
	require.Equal(t, 1, client.fetchCalls)
	require.True(t, response.Flushed)
	require.Contains(t, response.Body.String(), `"error":{"message":"stream failed"}`)
	require.NotContains(t, response.Body.String(), "private backend failure")
	var body struct {
		Logs  []json.RawMessage `json:"logs"`
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	require.Empty(t, body.Logs)
	require.Equal(t, "stream failed", body.Error.Message)
}
