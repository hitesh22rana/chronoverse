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

func (f *fakeJobsServiceClient) GetJobLogs(
	_ context.Context,
	req *jobspb.GetJobLogsRequest,
	_ ...grpc.CallOption,
) (
	*jobspb.GetJobLogsResponse,
	error,
) {
	f.getJobLogsRequest = req
	if f.getJobLogsResponse != nil {
		return f.getJobLogsResponse, nil
	}

	return &jobspb.GetJobLogsResponse{}, nil
}

func (f *fakeJobsServiceClient) SearchJobLogs(
	_ context.Context,
	req *jobspb.SearchJobLogsRequest,
	_ ...grpc.CallOption,
) (
	*jobspb.GetJobLogsResponse,
	error,
) {
	f.searchJobLogsRequest = req
	if f.searchJobLogsResponse != nil {
		return f.searchJobLogsResponse, nil
	}

	return &jobspb.GetJobLogsResponse{}, nil
}

func TestHandleGetJobTerminalReasonSerialization(t *testing.T) {
	tests := []struct {
		name       string
		response   *jobspb.GetJobResponse
		wantReason bool
	}{
		{
			name:       "failed reason is serialized",
			response:   &jobspb.GetJobResponse{Status: "FAILED", StatusReasonCode: "NON_ZERO_EXIT", StatusReasonMessage: "Process exited with non zero exit code"},
			wantReason: true,
		},
		{
			name:       "completed reason fields are omitted",
			response:   &jobspb.GetJobResponse{Status: "COMPLETED"},
			wantReason: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Server{jobsClient: &fakeJobsServiceClient{getJobResponse: tt.response}}
			res := httptest.NewRecorder()
			s.handleGetJob(res, newJobLogsHandlerRequest("/workflows/workflow_id/jobs/job_id"))
			if res.Code != http.StatusOK {
				t.Fatalf("status = %d", res.Code)
			}
			var body map[string]any
			if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			_, hasCode := body["status_reason_code"]
			_, hasMessage := body["status_reason_message"]
			if hasCode != tt.wantReason || hasMessage != tt.wantReason {
				t.Fatalf("reason presence = %v/%v, body=%s", hasCode, hasMessage, res.Body.String())
			}
			if _, exposed := body["last_error_message"]; exposed {
				t.Fatal("raw diagnostic field was exposed")
			}
		})
	}
}

func TestHandleDownloadJobLogsUsesAscendingGetLogs(t *testing.T) {
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
	if got := res.Body.String(); !strings.Contains(got, `"message_raw":"{\"id\":1}"`) || !strings.HasSuffix(got, "{\"type\":\"complete\"}\n") {
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
	if got := res.Body.String(); !strings.Contains(got, `"message_raw":"canceled job log"`) {
		t.Fatalf("unexpected body: %q", got)
	}
}

func TestHandleDownloadJobLogsUsesAscendingSearchWithoutHighlights(t *testing.T) {
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

	req := newJobLogsHandlerRequest("/workflows/workflow_id/jobs/job_id/logs/raw?q=error&stream=stderr")
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
	if err := json.Unmarshal([]byte(strings.Split(res.Body.String(), "\n")[0]), &line); err != nil {
		t.Fatalf("expected valid JSONL object: %v", err)
	}
	if line["message_raw"] != `{"id":1}` {
		t.Fatalf("unexpected message_raw: %v", line["message_raw"])
	}
	if line["type"] != "log" {
		t.Fatalf("unexpected record type: %v", line["type"])
	}
}

func TestHandleDownloadJobLogsCompletesEmptyExport(t *testing.T) {
	client := &fakeJobsServiceClient{
		getJobResponse:     &jobspb.GetJobResponse{Status: "COMPLETED"},
		getJobLogsResponse: &jobspb.GetJobLogsResponse{},
	}
	response := httptest.NewRecorder()
	(&Server{jobsClient: client}).handleDownloadJobLogs(response, newJobLogsHandlerRequest("/download"))
	if response.Code != http.StatusOK || response.Body.String() != "{\"type\":\"complete\"}\n" {
		t.Fatalf("empty export status=%d body=%q", response.Code, response.Body.String())
	}
	if response.Header().Get("Content-Type") != "application/x-ndjson" {
		t.Fatal("unexpected wire content type")
	}
}

func newJobLogsHandlerRequest(target string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, target, http.NoBody)
	req.SetPathValue("workflow_id", "workflow_id")
	req.SetPathValue("job_id", "550e8400-e29b-41d4-a716-446655440000")

	return req.WithContext(context.WithValue(req.Context(), userIDKey{}, "user_id"))
}

func TestHandleDownloadJobLogsRejectsNonUUIDJobID(t *testing.T) {
	client := &fakeJobsServiceClient{
		getJobResponse: &jobspb.GetJobResponse{Status: "COMPLETED"},
	}
	s := &Server{jobsClient: client}

	req := newJobLogsHandlerRequest("/workflows/workflow_id/jobs/job_id/logs/raw")
	req.SetPathValue("job_id", "job_id")
	res := httptest.NewRecorder()
	s.handleDownloadJobLogs(res, req)

	if res.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, res.Code)
	}
}
