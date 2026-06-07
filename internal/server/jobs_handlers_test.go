//nolint:testpackage // Tests unexported handlers directly.
package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"google.golang.org/grpc"

	jobspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"
)

type fakeJobsServiceClient struct {
	jobspb.JobsServiceClient

	getJobResponse        *jobspb.GetJobResponse
	getJobRequest         *jobspb.GetJobRequest
	getJobLogsResponse    *jobspb.GetJobLogsResponse
	getJobLogsRequest     *jobspb.GetJobLogsRequest
	searchJobLogsResponse *jobspb.GetJobLogsResponse
	searchJobLogsRequest  *jobspb.SearchJobLogsRequest
}

func (f *fakeJobsServiceClient) GetJob(_ context.Context, req *jobspb.GetJobRequest, _ ...grpc.CallOption) (*jobspb.GetJobResponse, error) {
	f.getJobRequest = req
	if f.getJobResponse != nil {
		return f.getJobResponse, nil
	}

	return &jobspb.GetJobResponse{Status: "COMPLETED"}, nil
}

func (f *fakeJobsServiceClient) GetJobLogs(_ context.Context, req *jobspb.GetJobLogsRequest, _ ...grpc.CallOption) (*jobspb.GetJobLogsResponse, error) {
	f.getJobLogsRequest = req
	if f.getJobLogsResponse != nil {
		return f.getJobLogsResponse, nil
	}

	return &jobspb.GetJobLogsResponse{}, nil
}

func (f *fakeJobsServiceClient) SearchJobLogs(_ context.Context, req *jobspb.SearchJobLogsRequest, _ ...grpc.CallOption) (*jobspb.GetJobLogsResponse, error) {
	f.searchJobLogsRequest = req
	if f.searchJobLogsResponse != nil {
		return f.searchJobLogsResponse, nil
	}

	return &jobspb.GetJobLogsResponse{}, nil
}

func TestHandleGetJobLogsCacheControl(t *testing.T) {
	tests := []struct {
		name           string
		target         string
		responseCursor string
		want           string
	}{
		{
			name:           "first page is not cached",
			target:         "/workflows/workflow_id/jobs/job_id/logs",
			responseCursor: "next-cursor",
			want:           "no-store",
		},
		{
			name:           "cursor page with another page is cached",
			target:         "/workflows/workflow_id/jobs/job_id/logs?cursor=current-cursor",
			responseCursor: "next-cursor",
			want:           "public, max-age=7200",
		},
		{
			name:           "cursor tail page is not cached",
			target:         "/workflows/workflow_id/jobs/job_id/logs?cursor=tail-cursor",
			responseCursor: "",
			want:           "no-store",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &fakeJobsServiceClient{
				getJobLogsResponse: &jobspb.GetJobLogsResponse{
					Cursor: tt.responseCursor,
				},
			}
			s := &Server{jobsClient: client}

			req := newJobLogsHandlerRequest(tt.target)
			res := httptest.NewRecorder()
			s.handleGetJobLogs(res, req)

			if res.Code != http.StatusOK {
				t.Fatalf("expected status %d, got %d", http.StatusOK, res.Code)
			}
			if got := res.Header().Get("Cache-Control"); got != tt.want {
				t.Fatalf("expected Cache-Control %q, got %q", tt.want, got)
			}
		})
	}
}

func TestHandleSearchJobLogsCacheControl(t *testing.T) {
	tests := []struct {
		name           string
		target         string
		responseCursor string
		want           string
	}{
		{
			name:           "first search page is not cached",
			target:         "/workflows/workflow_id/jobs/job_id/logs/search?q=message",
			responseCursor: "next-cursor",
			want:           "no-store",
		},
		{
			name:           "search cursor page with another page is cached",
			target:         "/workflows/workflow_id/jobs/job_id/logs/search?q=message&cursor=current-cursor",
			responseCursor: "next-cursor",
			want:           "public, max-age=7200",
		},
		{
			name:           "search cursor tail page is not cached",
			target:         "/workflows/workflow_id/jobs/job_id/logs/search?q=message&cursor=tail-cursor",
			responseCursor: "",
			want:           "no-store",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &fakeJobsServiceClient{
				searchJobLogsResponse: &jobspb.GetJobLogsResponse{
					Cursor: tt.responseCursor,
				},
			}
			s := &Server{jobsClient: client}

			req := newJobLogsHandlerRequest(tt.target)
			res := httptest.NewRecorder()
			s.handleSearchJobLogs(res, req)

			if res.Code != http.StatusOK {
				t.Fatalf("expected status %d, got %d", http.StatusOK, res.Code)
			}
			if got := res.Header().Get("Cache-Control"); got != tt.want {
				t.Fatalf("expected Cache-Control %q, got %q", tt.want, got)
			}
			if client.searchJobLogsRequest == nil {
				t.Fatal("expected SearchJobLogs to be called")
			}
		})
	}
}

func TestHandleDownloadJobLogsRawTextUsesAscendingGetLogs(t *testing.T) {
	client := &fakeJobsServiceClient{
		getJobResponse: &jobspb.GetJobResponse{Status: "COMPLETED"},
		getJobLogsResponse: &jobspb.GetJobLogsResponse{
			Logs: []*jobspb.Log{
				{
					Timestamp:   "2026-06-05T12:00:00Z",
					Message:     `{"id":1}`,
					SequenceNum: 1,
					Stream:      "stdout",
					EventId:     "event-1",
				},
			},
		},
	}
	s := &Server{jobsClient: client}

	req := newJobLogsHandlerRequest("/workflows/workflow_id/jobs/job_id/logs/raw?stream=stdout")
	res := httptest.NewRecorder()
	s.handleDownloadJobLogs(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, res.Code)
	}
	if got := res.Body.String(); got != "{\"id\":1}\n" {
		t.Fatalf("unexpected body: %q", got)
	}
	if client.getJobLogsRequest == nil {
		t.Fatal("expected GetJobLogs to be called")
	}
	if client.getJobLogsRequest.GetSortOrder() != jobspb.LogSortOrder_LOG_SORT_ORDER_ASC {
		t.Fatalf("unexpected sort order: %v", client.getJobLogsRequest.GetSortOrder())
	}
	if client.getJobLogsRequest.GetFilters().GetStream() != jobspb.LogStream_LOG_STREAM_STDOUT {
		t.Fatalf("unexpected stream: %v", client.getJobLogsRequest.GetFilters().GetStream())
	}
}

func TestHandleDownloadJobLogsAllowsCanceledJobWithLogs(t *testing.T) {
	client := &fakeJobsServiceClient{
		getJobResponse: &jobspb.GetJobResponse{Status: "CANCELED"},
		getJobLogsResponse: &jobspb.GetJobLogsResponse{
			Logs: []*jobspb.Log{
				{
					Message:     "canceled job log",
					SequenceNum: 1,
					Stream:      "stdout",
					EventId:     "event-1",
				},
			},
		},
	}
	s := &Server{jobsClient: client}

	req := newJobLogsHandlerRequest("/workflows/workflow_id/jobs/job_id/logs/raw")
	res := httptest.NewRecorder()
	s.handleDownloadJobLogs(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, res.Code)
	}
	if got := res.Body.String(); got != "canceled job log\n" {
		t.Fatalf("unexpected body: %q", got)
	}
}

func TestHandleDownloadJobLogsSearchJSONLUsesAscendingSearchWithoutHighlights(t *testing.T) {
	client := &fakeJobsServiceClient{
		getJobResponse: &jobspb.GetJobResponse{Status: "FAILED"},
		searchJobLogsResponse: &jobspb.GetJobLogsResponse{
			Logs: []*jobspb.Log{
				{
					Timestamp:   "2026-06-05T12:00:00Z",
					Message:     `{"id":1}`,
					SequenceNum: 1,
					Stream:      "stderr",
					EventId:     "event-1",
				},
			},
		},
	}
	s := &Server{jobsClient: client}

	req := newJobLogsHandlerRequest("/workflows/workflow_id/jobs/job_id/logs/raw?format=jsonl&q=error&stream=stderr")
	res := httptest.NewRecorder()
	s.handleDownloadJobLogs(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, res.Code)
	}
	if client.searchJobLogsRequest == nil {
		t.Fatal("expected SearchJobLogs to be called")
	}
	if client.searchJobLogsRequest.GetSortOrder() != jobspb.LogSortOrder_LOG_SORT_ORDER_ASC {
		t.Fatalf("unexpected sort order: %v", client.searchJobLogsRequest.GetSortOrder())
	}
	if !client.searchJobLogsRequest.GetDisableHighlight() {
		t.Fatal("expected highlights to be disabled")
	}
	if client.searchJobLogsRequest.GetFilters().GetMessage() != "error" {
		t.Fatalf("unexpected query: %q", client.searchJobLogsRequest.GetFilters().GetMessage())
	}
	if client.searchJobLogsRequest.GetFilters().GetStream() != jobspb.LogStream_LOG_STREAM_STDERR {
		t.Fatalf("unexpected stream: %v", client.searchJobLogsRequest.GetFilters().GetStream())
	}

	var line map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(res.Body.String())), &line); err != nil {
		t.Fatalf("expected valid JSONL object: %v", err)
	}
	if line["message_raw"] != `{"id":1}` {
		t.Fatalf("unexpected message_raw: %v", line["message_raw"])
	}
	if _, ok := line["message_json"]; !ok {
		t.Fatal("expected message_json for parseable JSON message")
	}
}

func TestHandleDownloadJobLogsJSONEscapesFilterControlCharacters(t *testing.T) {
	client := &fakeJobsServiceClient{
		getJobResponse:        &jobspb.GetJobResponse{Status: "COMPLETED"},
		searchJobLogsResponse: &jobspb.GetJobLogsResponse{},
	}
	s := &Server{jobsClient: client}

	req := newJobLogsHandlerRequest("/workflows/workflow_id/jobs/job_id/logs/raw?format=json&q=%1F")
	res := httptest.NewRecorder()
	s.handleDownloadJobLogs(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, res.Code)
	}

	var body struct {
		Filters struct {
			Query string `json:"q"`
		} `json:"filters"`
		Logs []map[string]any `json:"logs"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatalf("expected valid JSON body, got %q: %v", res.Body.String(), err)
	}
	if body.Filters.Query != "\x1f" {
		t.Fatalf("unexpected query: %q", body.Filters.Query)
	}
	if len(body.Logs) != 0 {
		t.Fatalf("expected no logs, got %d", len(body.Logs))
	}
}

func TestHandleDownloadJobLogsRejectsInvalidFormat(t *testing.T) {
	client := &fakeJobsServiceClient{
		getJobResponse: &jobspb.GetJobResponse{Status: "COMPLETED"},
	}
	s := &Server{jobsClient: client}

	req := newJobLogsHandlerRequest("/workflows/workflow_id/jobs/job_id/logs/raw?format=csv")
	res := httptest.NewRecorder()
	s.handleDownloadJobLogs(res, req)

	if res.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, res.Code)
	}
}

func newJobLogsHandlerRequest(target string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, target, http.NoBody)
	req.SetPathValue("workflow_id", "workflow_id")
	req.SetPathValue("job_id", "job_id")

	return req.WithContext(context.WithValue(req.Context(), userIDKey{}, "user_id"))
}
