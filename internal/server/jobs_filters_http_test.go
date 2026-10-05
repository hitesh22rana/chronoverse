//nolint:testpackage // Tests the unexported jobs handlers through the real router.
package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	jobspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"
)

// recordingJobsClient records the jobs-service traffic a handler produced so
// tests can assert both what was forwarded and what was never reached.
type recordingJobsClient struct {
	jobspb.JobsServiceClient

	listCalls   int
	listReq     *jobspb.ListJobsRequest
	listResp    *jobspb.ListJobsResponse
	listErr     error
	getCalls    int
	getLogsCall int
	getLogsReq  *jobspb.GetJobLogsRequest
	getLogsResp *jobspb.GetJobLogsResponse
	getLogsErr  error
	searchCalls int
	searchReq   *jobspb.SearchJobLogsRequest
	searchResp  *jobspb.GetJobLogsResponse
	searchErr   error
	schedCalls  int
	schedReq    *jobspb.ScheduleJobRequest
	schedErr    error
}

func (c *recordingJobsClient) ListJobs(
	_ context.Context,
	req *jobspb.ListJobsRequest,
	_ ...grpc.CallOption,
) (*jobspb.ListJobsResponse, error) {
	c.listCalls++
	c.listReq = req
	if c.listErr != nil {
		return nil, c.listErr
	}
	if c.listResp != nil {
		return c.listResp, nil
	}
	return &jobspb.ListJobsResponse{}, nil
}

// GetJob only records the call. It exists so a guard regression is reported as
// a counted RPC instead of a panic through the nil-embedded client interface.
func (c *recordingJobsClient) GetJob(
	_ context.Context,
	_ *jobspb.GetJobRequest,
	_ ...grpc.CallOption,
) (*jobspb.GetJobResponse, error) {
	c.getCalls++
	return &jobspb.GetJobResponse{}, nil
}

func (c *recordingJobsClient) GetJobLogs(
	_ context.Context,
	req *jobspb.GetJobLogsRequest,
	_ ...grpc.CallOption,
) (*jobspb.GetJobLogsResponse, error) {
	c.getLogsCall++
	c.getLogsReq = req
	if c.getLogsErr != nil {
		return nil, c.getLogsErr
	}
	if c.getLogsResp != nil {
		return c.getLogsResp, nil
	}
	return &jobspb.GetJobLogsResponse{}, nil
}

func (c *recordingJobsClient) SearchJobLogs(
	_ context.Context,
	req *jobspb.SearchJobLogsRequest,
	_ ...grpc.CallOption,
) (*jobspb.GetJobLogsResponse, error) {
	c.searchCalls++
	c.searchReq = req
	if c.searchErr != nil {
		return nil, c.searchErr
	}
	if c.searchResp != nil {
		return c.searchResp, nil
	}
	return &jobspb.GetJobLogsResponse{}, nil
}

func (c *recordingJobsClient) ScheduleJob(
	_ context.Context,
	req *jobspb.ScheduleJobRequest,
	_ ...grpc.CallOption,
) (*jobspb.ScheduleJobResponse, error) {
	c.schedCalls++
	c.schedReq = req
	if c.schedErr != nil {
		return nil, c.schedErr
	}
	return &jobspb.ScheduleJobResponse{}, nil
}

// logsCalls returns the total number of logs RPCs the handler produced.
func (c *recordingJobsClient) logsCalls() int {
	return c.getLogsCall + c.searchCalls
}

// newJobsRouter serves the jobs routes through a real http.ServeMux so handlers
// observe path values produced by stdlib routing.
func newJobsRouter(s *Server) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/workflows/{workflow_id}/jobs", s.handleListJobs)
	mux.HandleFunc("/workflows/{workflow_id}/jobs/{job_id}/logs", s.handleGetJobLogs)
	mux.HandleFunc("/workflows/{workflow_id}/jobs/{job_id}/logs/search", s.handleSearchJobLogs)
	return mux
}

func newJobsRequest(method, target, userID string) *http.Request {
	req := httptest.NewRequest(method, target, http.NoBody)
	if userID == "" {
		return req
	}
	return req.WithContext(context.WithValue(req.Context(), userIDKey{}, userID))
}

// TestHandleListJobsRejectsMalformedFilters pins the concrete 400 body for
// rejected list filters and proves ListJobs is never reached.
func TestHandleListJobsRejectsMalformedFilters(t *testing.T) {
	tests := []struct {
		name    string
		target  string
		wantMsg string
	}{
		{
			name:    "unknown status",
			target:  "/workflows/wf-1/jobs?status=DONE",
			wantMsg: "invalid status\n",
		},
		{
			name:    "status is case sensitive",
			target:  "/workflows/wf-1/jobs?status=completed",
			wantMsg: "invalid status\n",
		},
		{
			name:    "unknown trigger",
			target:  "/workflows/wf-1/jobs?trigger=SCHEDULED",
			wantMsg: "invalid trigger\n",
		},
		{
			name:    "status is rejected before trigger",
			target:  "/workflows/wf-1/jobs?status=DONE&trigger=SCHEDULED",
			wantMsg: "invalid status\n",
		},
		{
			name:    "malformed status is rejected even with a cursor",
			target:  "/workflows/wf-1/jobs?cursor=opaque&status=DONE",
			wantMsg: "invalid status\n",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &recordingJobsClient{}
			s := &Server{jobsClient: client}

			res := httptest.NewRecorder()
			newJobsRouter(s).ServeHTTP(res, newJobsRequest(http.MethodGet, test.target, "user-1"))

			if res.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d (body %q)", res.Code, http.StatusBadRequest, res.Body.String())
			}
			if got := res.Body.String(); got != test.wantMsg {
				t.Fatalf("body = %q, want %q", got, test.wantMsg)
			}
			if client.listCalls != 0 {
				t.Fatalf("ListJobs called %d times for a rejected request", client.listCalls)
			}
		})
	}
}

// TestHandleListJobsForwardsFiltersAndCursor verifies the filter/cursor payload
// the handler forwards, including opaque cursors with base64 padding.
func TestHandleListJobsForwardsFiltersAndCursor(t *testing.T) {
	tests := []struct {
		name       string
		target     string
		wantStatus string
		wantTrig   string
		wantCursor string
	}{
		{name: "no filters", target: "/workflows/wf-1/jobs"},
		{name: "empty status behaves like an absent status", target: "/workflows/wf-1/jobs?status="},
		{name: "empty trigger behaves like an absent trigger", target: "/workflows/wf-1/jobs?trigger="},
		{name: "status only", target: "/workflows/wf-1/jobs?status=FAILED", wantStatus: "FAILED"},
		{name: "trigger only", target: "/workflows/wf-1/jobs?trigger=MANUAL", wantTrig: "MANUAL"},
		{
			name:       "status and trigger",
			target:     "/workflows/wf-1/jobs?status=RUNNING&trigger=AUTOMATIC",
			wantStatus: "RUNNING",
			wantTrig:   "AUTOMATIC",
		},
		{
			name:       "opaque cursor is forwarded verbatim",
			target:     "/workflows/wf-1/jobs?cursor=eyJvIjo0MH0%3D",
			wantCursor: "eyJvIjo0MH0=",
		},
		{
			name:   "empty cursor behaves like an absent cursor",
			target: "/workflows/wf-1/jobs?cursor=",
		},
		{
			name:       "cursor travels with filters",
			target:     "/workflows/wf-1/jobs?status=COMPLETED&cursor=abc%2Fdef%3D",
			wantStatus: "COMPLETED",
			wantCursor: "abc/def=",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &recordingJobsClient{}
			s := &Server{jobsClient: client}

			res := httptest.NewRecorder()
			newJobsRouter(s).ServeHTTP(res, newJobsRequest(http.MethodGet, test.target, "user-1"))

			if res.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d (body %q)", res.Code, http.StatusOK, res.Body.String())
			}
			if client.listCalls != 1 {
				t.Fatalf("ListJobs calls = %d, want 1", client.listCalls)
			}
			if got := client.listReq.GetWorkflowId(); got != "wf-1" {
				t.Fatalf("workflow_id = %q, want %q", got, "wf-1")
			}
			if got := client.listReq.GetUserId(); got != "user-1" {
				t.Fatalf("user_id = %q, want %q", got, "user-1")
			}
			if got := client.listReq.GetCursor(); got != test.wantCursor {
				t.Fatalf("cursor = %q, want %q", got, test.wantCursor)
			}
			if client.listReq.Filters == nil {
				t.Fatal("expected a non-nil filter message")
			}
			if got := client.listReq.GetFilters().GetStatus(); got != test.wantStatus {
				t.Fatalf("status filter = %q, want %q", got, test.wantStatus)
			}
			if got := client.listReq.GetFilters().GetTrigger(); got != test.wantTrig {
				t.Fatalf("trigger filter = %q, want %q", got, test.wantTrig)
			}
			if got := res.Header().Get("Content-Type"); got != testJSONContentType {
				t.Fatalf("Content-Type = %q, want %s", got, testJSONContentType)
			}
		})
	}
}

// TestHandleListJobsCursorRoundTrip verifies the cursor the backend returns is
// accepted verbatim as the next page cursor.
func TestHandleListJobsCursorRoundTrip(t *testing.T) {
	const nextCursor = "eyJvZmZzZXQiOjEwfQ=="

	client := &recordingJobsClient{listResp: &jobspb.ListJobsResponse{Cursor: nextCursor}}
	s := &Server{jobsClient: client}

	res := httptest.NewRecorder()
	newJobsRouter(s).ServeHTTP(res, newJobsRequest(http.MethodGet, "/workflows/wf-1/jobs", "user-1"))
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusOK)
	}

	var body struct {
		Cursor string `json:"cursor"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body %q: %v", res.Body.String(), err)
	}
	if body.Cursor != nextCursor {
		t.Fatalf("response cursor = %q, want %q", body.Cursor, nextCursor)
	}

	next := httptest.NewRecorder()
	target := "/workflows/wf-1/jobs?status=FAILED&cursor=" + nextCursor
	newJobsRouter(s).ServeHTTP(next, newJobsRequest(http.MethodGet, target, "user-1"))
	if next.Code != http.StatusOK {
		t.Fatalf("paged status = %d, want %d", next.Code, http.StatusOK)
	}
	if client.listCalls != 2 {
		t.Fatalf("ListJobs calls = %d, want 2", client.listCalls)
	}
	if got := client.listReq.GetCursor(); got != nextCursor {
		t.Fatalf("paged cursor = %q, want %q", got, nextCursor)
	}
	if got := client.listReq.GetFilters().GetStatus(); got != "FAILED" {
		t.Fatalf("paged status filter = %q, want %q", got, "FAILED")
	}
}

// TestHandleListJobsRejectsMissingIdentifiers covers the guards that keep an
// unauthenticated or unrouted request away from the backend.
func TestHandleListJobsRejectsMissingIdentifiers(t *testing.T) {
	t.Run("missing workflow id", func(t *testing.T) {
		client := &recordingJobsClient{}
		s := &Server{jobsClient: client}

		req := newJobsRequest(http.MethodGet, "/workflows/wf-1/jobs", "user-1")
		req.SetPathValue("workflow_id", "")

		res := httptest.NewRecorder()
		s.handleListJobs(res, req)

		if res.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d", res.Code, http.StatusBadRequest)
		}
		if got := res.Body.String(); got != "job ID not found\n" {
			t.Fatalf("body = %q, want %q", got, "job ID not found\n")
		}
		if client.listCalls != 0 {
			t.Fatalf("ListJobs called %d times", client.listCalls)
		}
	})

	t.Run("missing authenticated user", func(t *testing.T) {
		client := &recordingJobsClient{}
		s := &Server{jobsClient: client}

		res := httptest.NewRecorder()
		newJobsRouter(s).ServeHTTP(res, newJobsRequest(http.MethodGet, "/workflows/wf-1/jobs", ""))

		if res.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d", res.Code, http.StatusBadRequest)
		}
		if got := res.Body.String(); got != "user ID not found\n" {
			t.Fatalf("body = %q, want %q", got, "user ID not found\n")
		}
		if client.listCalls != 0 {
			t.Fatalf("ListJobs called %d times without an authenticated user", client.listCalls)
		}
	})
}

// TestHandleListJobsBackendErrorHidesDetail verifies the concrete status of
// every mapped backend failure and that internal gRPC detail never leaks.
func TestHandleListJobsBackendErrorHidesDetail(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		wantCode int
	}{
		{name: "not found", err: status.Error(codes.NotFound, "workflow not found"), wantCode: http.StatusNotFound},
		{name: "failed precondition", err: status.Error(codes.FailedPrecondition, "workflow is terminated"), wantCode: http.StatusPreconditionFailed},
		{name: "resource exhausted", err: status.Error(codes.ResourceExhausted, "rate limited"), wantCode: http.StatusTooManyRequests},
		{name: "deadline exceeded", err: status.Error(codes.DeadlineExceeded, "context deadline"), wantCode: http.StatusGatewayTimeout},
		{name: "canceled", err: status.Error(codes.Canceled, "client went away"), wantCode: http.StatusRequestTimeout},
		{name: "unimplemented", err: status.Error(codes.Unimplemented, "method missing"), wantCode: http.StatusNotImplemented},
		{name: "already exists", err: status.Error(codes.AlreadyExists, "duplicate"), wantCode: http.StatusConflict},
		{name: "aborted", err: status.Error(codes.Aborted, "concurrent update"), wantCode: http.StatusConflict},
		{name: "out of range", err: status.Error(codes.OutOfRange, "bad page"), wantCode: http.StatusInternalServerError},
		{name: "data loss", err: status.Error(codes.DataLoss, "checksum"), wantCode: http.StatusInternalServerError},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &recordingJobsClient{listErr: test.err}
			s := &Server{jobsClient: client}

			res := httptest.NewRecorder()
			newJobsRouter(s).ServeHTTP(res, newJobsRequest(http.MethodGet, "/workflows/wf-1/jobs", "user-1"))

			if res.Code != test.wantCode {
				t.Fatalf("status = %d, want %d", res.Code, test.wantCode)
			}
			if got := res.Body.String(); got != "failed to list jobs\n" {
				t.Fatalf("body = %q, want %q", got, "failed to list jobs\n")
			}
		})
	}
}

// TestHandleSearchJobLogsRejectsMalformedStream pins the concrete 400 body for
// rejected stream filters and proves neither logs RPC is reached.
func TestHandleSearchJobLogsRejectsMalformedStream(t *testing.T) {
	tests := []struct {
		name   string
		target string
	}{
		{name: "unknown stream", target: "/workflows/wf-1/jobs/job-1/logs/search?stream=stdinfo"},
		{name: "stream is case sensitive", target: "/workflows/wf-1/jobs/job-1/logs/search?stream=STDOUT"},
		{name: "invalid stream with query", target: "/workflows/wf-1/jobs/job-1/logs/search?stream=nope&q=error"},
		{name: "invalid stream with cursor", target: "/workflows/wf-1/jobs/job-1/logs/search?stream=nope&cursor=abc"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &recordingJobsClient{}
			s := &Server{jobsClient: client}

			res := httptest.NewRecorder()
			newJobsRouter(s).ServeHTTP(res, newJobsRequest(http.MethodGet, test.target, "user-1"))

			if res.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d (body %q)", res.Code, http.StatusBadRequest, res.Body.String())
			}
			if got := res.Body.String(); got != "invalid log stream type\n" {
				t.Fatalf("body = %q, want %q", got, "invalid log stream type\n")
			}
			if client.logsCalls() != 0 {
				t.Fatalf("logs RPCs reached the backend: %d", client.logsCalls())
			}
		})
	}
}

// TestHandleSearchJobLogsRoutesStreamAndCursor verifies which logs RPC each
// stream/query/cursor combination selects, what it forwards, and how the
// resulting Cache-Control depends on the cursor pair.
func TestHandleSearchJobLogsRoutesStreamAndCursor(t *testing.T) {
	const opaqueCursor = "eyJvIjoyMH0="

	tests := []struct {
		name        string
		target      string
		wantSearch  bool
		wantStream  jobspb.LogStream
		wantMessage string
		wantCursor  string
		wantCaching string
	}{
		{
			name:        "no query no stream uses get logs with all streams",
			target:      "/workflows/wf-1/jobs/job-1/logs/search",
			wantStream:  jobspb.LogStream_LOG_STREAM_ALL,
			wantMessage: "",
			wantCaching: "no-store",
		},
		{
			name:        "empty query value behaves like an absent query",
			target:      "/workflows/wf-1/jobs/job-1/logs/search?q=",
			wantStream:  jobspb.LogStream_LOG_STREAM_ALL,
			wantMessage: "",
			wantCaching: "no-store",
		},
		{
			name:        "stdout filter without query",
			target:      "/workflows/wf-1/jobs/job-1/logs/search?stream=stdout",
			wantStream:  jobspb.LogStream_LOG_STREAM_STDOUT,
			wantMessage: "",
			wantCaching: "no-store",
		},
		{
			name:        "query switches to the search rpc",
			target:      "/workflows/wf-1/jobs/job-1/logs/search?q=panic",
			wantSearch:  true,
			wantStream:  jobspb.LogStream_LOG_STREAM_ALL,
			wantMessage: "panic",
			wantCaching: "no-store",
		},
		{
			name:        "query and stream combine on the search rpc",
			target:      "/workflows/wf-1/jobs/job-1/logs/search?q=panic&stream=stderr",
			wantSearch:  true,
			wantStream:  jobspb.LogStream_LOG_STREAM_STDERR,
			wantMessage: "panic",
			wantCaching: "no-store",
		},
		{
			name:        "whitespace query still searches",
			target:      "/workflows/wf-1/jobs/job-1/logs/search?q=%20",
			wantSearch:  true,
			wantStream:  jobspb.LogStream_LOG_STREAM_ALL,
			wantMessage: " ",
			wantCaching: "no-store",
		},
		{
			name:        "cursor is forwarded on the search rpc",
			target:      "/workflows/wf-1/jobs/job-1/logs/search?q=panic&cursor=" + opaqueCursor,
			wantSearch:  true,
			wantStream:  jobspb.LogStream_LOG_STREAM_ALL,
			wantMessage: "panic",
			wantCursor:  opaqueCursor,
			wantCaching: "private, max-age=7200",
		},
		{
			name:        "cursor is forwarded on the get rpc",
			target:      "/workflows/wf-1/jobs/job-1/logs/search?stream=stdout&cursor=" + opaqueCursor,
			wantStream:  jobspb.LogStream_LOG_STREAM_STDOUT,
			wantMessage: "",
			wantCursor:  opaqueCursor,
			wantCaching: "private, max-age=7200",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &recordingJobsClient{
				getLogsResp: &jobspb.GetJobLogsResponse{Cursor: "next-page"},
				searchResp:  &jobspb.GetJobLogsResponse{Cursor: "next-page"},
			}
			s := &Server{jobsClient: client}

			res := httptest.NewRecorder()
			newJobsRouter(s).ServeHTTP(res, newJobsRequest(http.MethodGet, test.target, "user-1"))

			if res.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d (body %q)", res.Code, http.StatusOK, res.Body.String())
			}

			assertLogsRouting(t, client, logsRoutingCase{
				wantSearch:  test.wantSearch,
				wantStream:  test.wantStream,
				wantMessage: test.wantMessage,
				wantCursor:  test.wantCursor,
			})

			if got := client.logsRequestIDs(); got != "wf-1/job-1/user-1" {
				t.Fatalf("forwarded ids = %q, want %q", got, "wf-1/job-1/user-1")
			}
			// Cacheability depends on both cursors: a first page must never be
			// cached even when the backend advertises another page.
			if got := res.Header().Get("Cache-Control"); got != test.wantCaching {
				t.Fatalf("Cache-Control = %q, want %q", got, test.wantCaching)
			}
		})
	}
}

// logsRoutingCase is the expected RPC selection for one query combination.
type logsRoutingCase struct {
	wantSearch  bool
	wantStream  jobspb.LogStream
	wantMessage string
	wantCursor  string
}

// assertLogsRouting pins which logs RPC the handler chose and the exact filter
// payload it forwarded for that choice.
func assertLogsRouting(t *testing.T, client *recordingJobsClient, test logsRoutingCase) {
	t.Helper()

	if test.wantSearch {
		if client.searchCalls != 1 || client.getLogsCall != 0 {
			t.Fatalf("rpc routing wrong: search=%d get=%d", client.searchCalls, client.getLogsCall)
		}
		if got := client.searchReq.GetCursor(); got != test.wantCursor {
			t.Fatalf("search cursor = %q, want %q", got, test.wantCursor)
		}
		if got := client.searchReq.GetFilters().GetMessage(); got != test.wantMessage {
			t.Fatalf("search message = %q, want %q", got, test.wantMessage)
		}
		if got := client.searchReq.GetFilters().GetStream(); got != test.wantStream {
			t.Fatalf("search stream = %v, want %v", got, test.wantStream)
		}
		return
	}

	if client.getLogsCall != 1 || client.searchCalls != 0 {
		t.Fatalf("rpc routing wrong: search=%d get=%d", client.searchCalls, client.getLogsCall)
	}
	if got := client.getLogsReq.GetCursor(); got != test.wantCursor {
		t.Fatalf("get cursor = %q, want %q", got, test.wantCursor)
	}
	if got := client.getLogsReq.GetFilters().GetStream(); got != test.wantStream {
		t.Fatalf("get stream = %v, want %v", got, test.wantStream)
	}
}

// logsRequestIDs renders the ids the selected logs RPC received.
func (c *recordingJobsClient) logsRequestIDs() string {
	if c.searchReq != nil {
		return c.searchReq.GetWorkflowId() + "/" + c.searchReq.GetId() + "/" + c.searchReq.GetUserId()
	}
	if c.getLogsReq != nil {
		return c.getLogsReq.GetWorkflowId() + "/" + c.getLogsReq.GetId() + "/" + c.getLogsReq.GetUserId()
	}
	return ""
}

// TestHandleGetJobLogsIgnoresStreamParameter verifies handleGetJobLogs always
// asks the backend for every stream, so a stray stream parameter in the query
// cannot narrow what the viewer shows. The cacheability contract for a cursor
// page is pinned once, by TestHandleGetJobLogsCacheControl.
func TestHandleGetJobLogsIgnoresStreamParameter(t *testing.T) {
	tests := []struct {
		name   string
		target string
	}{
		{
			name:   "no stream parameter",
			target: "/workflows/wf-1/jobs/job-1/logs",
		},
		{
			name:   "stream parameter is ignored",
			target: "/workflows/wf-1/jobs/job-1/logs?cursor=page-2&stream=stdout",
		},
		{
			name:   "stream parameter is ignored on a first page",
			target: "/workflows/wf-1/jobs/job-1/logs?stream=stderr",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &recordingJobsClient{getLogsResp: &jobspb.GetJobLogsResponse{Cursor: "next-page"}}
			s := &Server{jobsClient: client}

			res := httptest.NewRecorder()
			newJobsRouter(s).ServeHTTP(res, newJobsRequest(http.MethodGet, test.target, "user-1"))

			if res.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d", res.Code, http.StatusOK)
			}
			if got := client.getLogsReq.GetFilters().GetStream(); got != jobspb.LogStream_LOG_STREAM_ALL {
				t.Fatalf("stream filter = %v, want LOG_STREAM_ALL", got)
			}
		})
	}
}

// TestHandleSearchJobLogsRejectsMissingIdentifiers covers the guard ordering
// for log search requests that never reach the backend. The missing-workflow
// case documents that this handler answers "job ID not found" (unlike
// handleDownloadJobLogs, which says "workflow ID not found").
func TestHandleSearchJobLogsRejectsMissingIdentifiers(t *testing.T) {
	tests := []struct {
		name      string
		userID    string
		clearPath string
		wantMsg   string
	}{
		{name: "missing workflow id", userID: "user-1", clearPath: "workflow_id", wantMsg: "job ID not found\n"},
		{name: "missing job id", userID: "user-1", clearPath: "job_id", wantMsg: "job ID not found\n"},
		{name: "missing authenticated user", userID: "", wantMsg: "user ID not found\n"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &recordingJobsClient{}
			s := &Server{jobsClient: client}

			req := newJobsRequest(http.MethodGet, "/workflows/wf-1/jobs/job-1/logs/search?stream=stdout", test.userID)
			req.SetPathValue("workflow_id", "wf-1")
			req.SetPathValue("job_id", "job-1")
			if test.clearPath != "" {
				req.SetPathValue(test.clearPath, "")
			}

			res := httptest.NewRecorder()
			s.handleSearchJobLogs(res, req)

			if res.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d (body %q)", res.Code, http.StatusBadRequest, res.Body.String())
			}
			if got := res.Body.String(); got != test.wantMsg {
				t.Fatalf("body = %q, want %q", got, test.wantMsg)
			}
			if client.logsCalls() != 0 {
				t.Fatalf("logs RPCs reached the backend: %d", client.logsCalls())
			}
		})
	}
}

// TestHandleSearchJobLogsBackendErrorHidesDetail verifies both the get and the
// search failure paths share one generic message that leaks no backend detail.
func TestHandleSearchJobLogsBackendErrorHidesDetail(t *testing.T) {
	t.Run("get logs rpc", func(t *testing.T) {
		client := &recordingJobsClient{getLogsErr: status.Error(codes.PermissionDenied, "job belongs to user-2")}
		s := &Server{jobsClient: client}

		res := httptest.NewRecorder()
		newJobsRouter(s).ServeHTTP(res, newJobsRequest(http.MethodGet, "/workflows/wf-1/jobs/job-1/logs/search", "user-1"))

		if res.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want %d", res.Code, http.StatusForbidden)
		}
		if got := res.Body.String(); got != "failed to get job logs\n" {
			t.Fatalf("body = %q, want %q", got, "failed to get job logs\n")
		}
		if got := res.Header().Get("Cache-Control"); got != "" {
			t.Fatalf("error responses must not be cached, got Cache-Control %q", got)
		}
	})

	t.Run("search logs rpc", func(t *testing.T) {
		client := &recordingJobsClient{searchErr: status.Error(codes.Unavailable, "clickhouse unreachable: 10.0.0.4:9000")}
		s := &Server{jobsClient: client}

		res := httptest.NewRecorder()
		newJobsRouter(s).ServeHTTP(res, newJobsRequest(http.MethodGet, "/workflows/wf-1/jobs/job-1/logs/search?q=panic", "user-1"))

		if res.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want %d", res.Code, http.StatusServiceUnavailable)
		}
		if got := res.Body.String(); got != "failed to get job logs\n" {
			t.Fatalf("body = %q, want %q", got, "failed to get job logs\n")
		}
	})
}

// newScheduleRequest builds a manual-schedule request for the given user.
func newScheduleRequest(userID, idempotencyKey string) *http.Request {
	req := newJobsRequest(http.MethodPost, "/workflows/wf-1/jobs/schedule", userID)
	req.SetPathValue("workflow_id", "wf-1")
	if idempotencyKey != "" {
		req.Header.Set(idempotencyKeyHeader, idempotencyKey)
	}
	return req
}

// TestHandleManualScheduleJobRequiresIdempotencyKey proves ScheduleJob is never
// reached without a replay key, which would let a retried click double-schedule.
func TestHandleManualScheduleJobRequiresIdempotencyKey(t *testing.T) {
	t.Run("missing idempotency key", func(t *testing.T) {
		client := &recordingJobsClient{}
		s := &Server{jobsClient: client}

		res := httptest.NewRecorder()
		s.handleManualScheduleJob(res, newScheduleRequest("user-1", ""))

		if res.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d", res.Code, http.StatusBadRequest)
		}
		if got := res.Body.String(); got != "idempotency key is required\n" {
			t.Fatalf("body = %q, want %q", got, "idempotency key is required\n")
		}
		if client.schedCalls != 0 {
			t.Fatalf("ScheduleJob called %d times without an idempotency key", client.schedCalls)
		}
	})
}

// TestHandleManualScheduleJobForwardsManualTrigger verifies an accepted manual
// schedule is always stamped MANUAL with a parseable schedule time.
func TestHandleManualScheduleJobForwardsManualTrigger(t *testing.T) {
	t.Run("valid idempotency key schedules a manual job", func(t *testing.T) {
		client := &recordingJobsClient{}
		s := &Server{jobsClient: client}

		res := httptest.NewRecorder()
		s.handleManualScheduleJob(res, newScheduleRequest("user-1", "key-123"))

		if res.Code != http.StatusCreated {
			t.Fatalf("status = %d, want %d (body %q)", res.Code, http.StatusCreated, res.Body.String())
		}
		if client.schedCalls != 1 {
			t.Fatalf("ScheduleJob calls = %d, want 1", client.schedCalls)
		}
		if got := client.schedReq.GetWorkflowId(); got != "wf-1" {
			t.Fatalf("workflow_id = %q, want %q", got, "wf-1")
		}
		if got := client.schedReq.GetUserId(); got != "user-1" {
			t.Fatalf("user_id = %q, want %q", got, "user-1")
		}
		if got := client.schedReq.GetTrigger(); got != "MANUAL" {
			t.Fatalf("trigger = %q, want MANUAL", got)
		}
		if got := client.schedReq.GetIdempotencyKey(); got != "key-123" {
			t.Fatalf("idempotency key = %q, want %q", got, "key-123")
		}
		if _, err := time.Parse(time.RFC3339Nano, client.schedReq.GetScheduledAt()); err != nil {
			t.Fatalf("scheduled_at %q is not RFC3339Nano: %v", client.schedReq.GetScheduledAt(), err)
		}
	})
}

// TestHandleManualScheduleJobRejectsUnauthenticatedOrFailed pins the two
// remaining rejections: no session identity, and a failing jobs backend.
func TestHandleManualScheduleJobRejectsUnauthenticatedOrFailed(t *testing.T) {
	t.Run("missing authenticated user", func(t *testing.T) {
		client := &recordingJobsClient{}
		s := &Server{jobsClient: client}

		res := httptest.NewRecorder()
		s.handleManualScheduleJob(res, newScheduleRequest("", "key-123"))

		if res.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d", res.Code, http.StatusBadRequest)
		}
		if got := res.Body.String(); got != "user ID not found\n" {
			t.Fatalf("body = %q, want %q", got, "user ID not found\n")
		}
		if client.schedCalls != 0 {
			t.Fatalf("ScheduleJob called %d times without an authenticated user", client.schedCalls)
		}
	})

	t.Run("backend error hides detail", func(t *testing.T) {
		client := &recordingJobsClient{schedErr: status.Error(codes.FailedPrecondition, "workflow is terminated")}
		s := &Server{jobsClient: client}

		res := httptest.NewRecorder()
		s.handleManualScheduleJob(res, newScheduleRequest("user-1", "key-123"))

		if res.Code != http.StatusPreconditionFailed {
			t.Fatalf("status = %d, want %d", res.Code, http.StatusPreconditionFailed)
		}
		if got := res.Body.String(); got != "failed to schedule job\n" {
			t.Fatalf("body = %q, want %q", got, "failed to schedule job\n")
		}
	})
}
