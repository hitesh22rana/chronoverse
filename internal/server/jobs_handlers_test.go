//nolint:testpackage // Tests unexported handlers directly.
package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"google.golang.org/grpc"

	jobspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"
)

type fakeJobsServiceClient struct {
	jobspb.JobsServiceClient

	getJobLogsResponse    *jobspb.GetJobLogsResponse
	getJobLogsRequest     *jobspb.GetJobLogsRequest
	searchJobLogsResponse *jobspb.GetJobLogsResponse
	searchJobLogsRequest  *jobspb.SearchJobLogsRequest
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

func newJobLogsHandlerRequest(target string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, target, http.NoBody)
	req.SetPathValue("workflow_id", "workflow_id")
	req.SetPathValue("job_id", "job_id")

	return req.WithContext(context.WithValue(req.Context(), userIDKey{}, "user_id"))
}
