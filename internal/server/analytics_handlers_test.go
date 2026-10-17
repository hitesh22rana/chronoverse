//nolint:testpackage // Tests the unexported HTTP response adapters directly.
package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	analyticspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/analytics"
)

type fakeAnalyticsServiceClient struct {
	analyticspb.AnalyticsServiceClient

	getWorkflowAnalyticsError error
	userCall                  func(context.Context, *analyticspb.GetUserAnalyticsRequest) (*analyticspb.GetUserAnalyticsResponse, error)
}

func (f *fakeAnalyticsServiceClient) GetWorkflowAnalytics(
	_ context.Context, req *analyticspb.GetWorkflowAnalyticsRequest, _ ...grpc.CallOption,
) (*analyticspb.GetWorkflowAnalyticsResponse, error) {
	if f.getWorkflowAnalyticsError != nil {
		return nil, f.getWorkflowAnalyticsError
	}

	return &analyticspb.GetWorkflowAnalyticsResponse{WorkflowId: req.GetWorkflowId()}, nil
}

func (f *fakeAnalyticsServiceClient) GetUserAnalytics(ctx context.Context, req *analyticspb.GetUserAnalyticsRequest, _ ...grpc.CallOption) (*analyticspb.GetUserAnalyticsResponse, error) {
	if f.userCall == nil {
		return &analyticspb.GetUserAnalyticsResponse{}, nil
	}

	return f.userCall(ctx, req)
}

func TestHandleGetUserAnalyticsContracts(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name          string
		authenticated bool
		rpcErr        error
		wantStatus    int
	}{
		{"missing identity", false, nil, http.StatusBadRequest},
		{"success", true, nil, http.StatusOK},
		{"backend unavailable", true, status.Error(codes.Unavailable, "backend failed"), http.StatusServiceUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			calls := 0
			s := &Server{analyticsClient: &fakeAnalyticsServiceClient{userCall: func(_ context.Context, req *analyticspb.GetUserAnalyticsRequest) (*analyticspb.GetUserAnalyticsResponse, error) {
				calls++
				if !tc.authenticated || req.GetUserId() != "user-42" {
					t.Fatalf("unexpected RPC identity=%q", req.GetUserId())
				}
				return &analyticspb.GetUserAnalyticsResponse{TotalWorkflows: 7}, tc.rpcErr
			}}}
			request := httptest.NewRequest(http.MethodGet, "/analytics", http.NoBody)
			if tc.authenticated {
				request = request.WithContext(context.WithValue(t.Context(), userIDKey{}, "user-42"))
			}
			response := httptest.NewRecorder()
			s.handleGetUserAnalytics(response, request)
			if response.Code != tc.wantStatus {
				t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
			}
			wantCalls := 0
			if tc.authenticated {
				wantCalls = 1
			}
			if calls != wantCalls {
				t.Fatalf("calls=%d want=%d", calls, wantCalls)
			}
			switch tc.wantStatus {
			case http.StatusOK:
				want := `{"total_workflows":7,"total_jobs":0,"total_joblogs":0,"total_job_execution_duration":0,"workflow_kinds":[],"top_workflows":[]}` + "\n"
				if response.Body.String() != want || response.Header().Get("Content-Type") != testJSONContentType {
					t.Fatalf("response headers=%v body=%q", response.Header(), response.Body.String())
				}
			case http.StatusBadRequest:
				if response.Body.String() != "user ID not found\n" {
					t.Fatalf("body=%q", response.Body.String())
				}
			case http.StatusServiceUnavailable:
				if response.Body.String() != "failed to get user analytics\n" {
					t.Fatalf("body=%q", response.Body.String())
				}
			}
		})
	}
}

// TestHandleGetWorkflowAnalyticsRejectsMissingWorkflowID pins the guard: an
// empty path value never reaches the analytics backend.
func TestHandleGetWorkflowAnalyticsRejectsMissingWorkflowID(t *testing.T) {
	t.Parallel()

	s := &Server{analyticsClient: &fakeAnalyticsServiceClient{}}

	response := httptest.NewRecorder()
	s.handleGetWorkflowAnalytics(response, httptest.NewRequest(http.MethodGet, "/analytics", http.NoBody))

	if response.Code != http.StatusBadRequest || response.Body.String() != "workflow ID not found\n" {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
}

// TestHandleGetWorkflowAnalyticsMapsRPCError covers the backend failure mapping.
func TestHandleGetWorkflowAnalyticsMapsRPCError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		code       codes.Code
		wantStatus int
	}{
		{"not found", codes.NotFound, http.StatusNotFound},
		{"backend unavailable", codes.Unavailable, http.StatusServiceUnavailable},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s := &Server{analyticsClient: &fakeAnalyticsServiceClient{
				getWorkflowAnalyticsError: status.Error(tt.code, "backend failed"),
			}}

			request := httptest.NewRequest(http.MethodGet, "/analytics/workflow-1", http.NoBody).
				WithContext(context.WithValue(t.Context(), userIDKey{}, "user-42"))
			request.SetPathValue("workflow_id", "workflow-1")

			response := httptest.NewRecorder()
			s.handleGetWorkflowAnalytics(response, request)

			if response.Code != tt.wantStatus || response.Body.String() != "failed to get workflow analytics\n" {
				t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
			}
		})
	}
}

func TestUserAnalyticsHTTPResponseEmitsZeroValues(t *testing.T) {
	t.Parallel()

	encoded, err := json.Marshal(newUserAnalyticsHTTPResponse(&analyticspb.GetUserAnalyticsResponse{}))
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}

	want := `{"total_workflows":0,"total_jobs":0,"total_joblogs":0,"total_job_execution_duration":0,"workflow_kinds":[],"top_workflows":[]}`
	if string(encoded) != want {
		t.Fatalf("got %s, want %s", encoded, want)
	}
}

func TestUserAnalyticsHTTPResponseEmitsNestedZeroValues(t *testing.T) {
	t.Parallel()

	response := newUserAnalyticsHTTPResponse(&analyticspb.GetUserAnalyticsResponse{
		WorkflowKinds: []*analyticspb.WorkflowKindAnalytics{{Kind: workflowKindContainer}},
		TopWorkflows: []*analyticspb.WorkflowAnalyticsSummary{{
			WorkflowId:   "workflow-id",
			WorkflowName: "Example",
			Kind:         workflowKindContainer,
		}},
	})

	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}

	want := `{"total_workflows":0,"total_jobs":0,"total_joblogs":0,"total_job_execution_duration":0,` +
		`"workflow_kinds":[{"kind":"CONTAINER","total_workflows":0,"total_jobs":0,"total_joblogs":0,` +
		`"total_job_execution_duration":0}],"top_workflows":[{"workflow_id":"workflow-id",` +
		`"workflow_name":"Example","kind":"CONTAINER","total_jobs":0,"total_joblogs":0,` +
		`"total_job_execution_duration":0}]}`
	if string(encoded) != want {
		t.Fatalf("got %s, want %s", encoded, want)
	}
}

func TestWorkflowAnalyticsHTTPResponseEmitsZeroValues(t *testing.T) {
	t.Parallel()

	encoded, err := json.Marshal(newWorkflowAnalyticsHTTPResponse(
		&analyticspb.GetWorkflowAnalyticsResponse{WorkflowId: "workflow-id"},
	))
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}

	want := `{"workflow_id":"workflow-id","total_job_execution_duration":0,"total_jobs":0,"total_joblogs":0}`
	if string(encoded) != want {
		t.Fatalf("got %s, want %s", encoded, want)
	}
}
