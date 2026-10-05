//nolint:testpackage // Tests the unexported list-workflows handler through the real router.
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

	workflowspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/workflows"
)

// recordingWorkflowsClient records the workflows-service traffic a handler
// produced so tests can assert both what was forwarded and what was never
// reached. Every RPC a session-guarded handler can reach is implemented, so a
// guard regression surfaces as a counted call rather than a panic through the
// nil-embedded client interface.
type recordingWorkflowsClient struct {
	workflowspb.WorkflowsServiceClient

	listCalls      int
	listReq        *workflowspb.ListWorkflowsRequest
	listResp       *workflowspb.ListWorkflowsResponse
	listErr        error
	getCalls       int
	createCalls    int
	updateCalls    int
	terminateCalls int
	deleteCalls    int
}

func (c *recordingWorkflowsClient) ListWorkflows(
	_ context.Context,
	req *workflowspb.ListWorkflowsRequest,
	_ ...grpc.CallOption,
) (*workflowspb.ListWorkflowsResponse, error) {
	c.listCalls++
	c.listReq = req
	if c.listErr != nil {
		return nil, c.listErr
	}
	if c.listResp != nil {
		return c.listResp, nil
	}
	return &workflowspb.ListWorkflowsResponse{}, nil
}

func (c *recordingWorkflowsClient) GetWorkflow(
	_ context.Context,
	_ *workflowspb.GetWorkflowRequest,
	_ ...grpc.CallOption,
) (*workflowspb.GetWorkflowResponse, error) {
	c.getCalls++
	return &workflowspb.GetWorkflowResponse{}, nil
}

func (c *recordingWorkflowsClient) CreateWorkflow(
	_ context.Context,
	_ *workflowspb.CreateWorkflowRequest,
	_ ...grpc.CallOption,
) (*workflowspb.CreateWorkflowResponse, error) {
	c.createCalls++
	return &workflowspb.CreateWorkflowResponse{}, nil
}

func (c *recordingWorkflowsClient) UpdateWorkflow(
	_ context.Context,
	_ *workflowspb.UpdateWorkflowRequest,
	_ ...grpc.CallOption,
) (*workflowspb.UpdateWorkflowResponse, error) {
	c.updateCalls++
	return &workflowspb.UpdateWorkflowResponse{}, nil
}

func (c *recordingWorkflowsClient) TerminateWorkflow(
	_ context.Context,
	_ *workflowspb.TerminateWorkflowRequest,
	_ ...grpc.CallOption,
) (*workflowspb.TerminateWorkflowResponse, error) {
	c.terminateCalls++
	return &workflowspb.TerminateWorkflowResponse{}, nil
}

func (c *recordingWorkflowsClient) DeleteWorkflow(
	_ context.Context,
	_ *workflowspb.DeleteWorkflowRequest,
	_ ...grpc.CallOption,
) (*workflowspb.DeleteWorkflowResponse, error) {
	c.deleteCalls++
	return &workflowspb.DeleteWorkflowResponse{}, nil
}

// newListWorkflowsRouter serves GET /workflows through a real http.ServeMux so
// the handler observes path values produced by stdlib routing.
func newListWorkflowsRouter(s *Server) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/workflows", s.handleListWorkflows)
	return mux
}

func newWorkflowsListRequest(target, userID string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, target, http.NoBody)
	if userID == "" {
		return req
	}
	return req.WithContext(context.WithValue(req.Context(), userIDKey{}, userID))
}

// TestHandleListWorkflowsRejectsMalformedFilters pins the concrete 400 body for
// every rejected filter combination and proves the workflows backend is never
// called, so a malformed query can never reach the service.
func TestHandleListWorkflowsRejectsMalformedFilters(t *testing.T) {
	tests := []struct {
		name    string
		target  string
		wantMsg string
	}{
		{
			name:    "unknown kind",
			target:  "/workflows?kind=BATCH",
			wantMsg: "invalid kind\n",
		},
		{
			name:    "kind is case sensitive",
			target:  "/workflows?kind=container",
			wantMsg: "invalid kind\n",
		},
		{
			name:    "unknown build status",
			target:  "/workflows?build_status=DONE",
			wantMsg: "invalid build status\n",
		},
		{
			name:    "terminated is not a boolean",
			target:  "/workflows?terminated=yes",
			wantMsg: "invalid terminated\n",
		},
		{
			name:    "terminated true conflicts with build status",
			target:  "/workflows?build_status=COMPLETED&terminated=true",
			wantMsg: "terminated cannot be true when build status is provided\n",
		},
		{
			name:    "negative interval_min",
			target:  "/workflows?interval_min=-1",
			wantMsg: "invalid interval_min\n",
		},
		{
			name:    "non numeric interval_min",
			target:  "/workflows?interval_min=hourly",
			wantMsg: "invalid interval_min\n",
		},
		{
			name:    "interval_min overflows int32",
			target:  "/workflows?interval_min=2147483648",
			wantMsg: "invalid interval_min\n",
		},
		{
			name:    "negative interval_max",
			target:  "/workflows?interval_max=-30",
			wantMsg: "invalid interval_max\n",
		},
		{
			name:    "interval_max below interval_min",
			target:  "/workflows?interval_min=60&interval_max=30",
			wantMsg: "invalid interval_max\n",
		},
		{
			name:    "interval_max overflows int32",
			target:  "/workflows?interval_max=2147483648",
			wantMsg: "invalid interval_max\n",
		},
		{
			name:    "kind is rejected before build status",
			target:  "/workflows?kind=BATCH&build_status=DONE",
			wantMsg: "invalid kind\n",
		},
		{
			name:    "build status is rejected before terminated",
			target:  "/workflows?build_status=DONE&terminated=maybe",
			wantMsg: "invalid build status\n",
		},
		{
			name:    "terminated is rejected before interval bounds",
			target:  "/workflows?terminated=maybe&interval_min=-5",
			wantMsg: "invalid terminated\n",
		},
		{
			name:    "malformed filter is rejected even with a cursor",
			target:  "/workflows?cursor=opaque-cursor&kind=BATCH",
			wantMsg: "invalid kind\n",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &recordingWorkflowsClient{}
			s := &Server{workflowsClient: client}

			res := httptest.NewRecorder()
			newListWorkflowsRouter(s).ServeHTTP(res, newWorkflowsListRequest(test.target, "user-1"))

			if res.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d (body %q)", res.Code, http.StatusBadRequest, res.Body.String())
			}
			if got := res.Body.String(); got != test.wantMsg {
				t.Fatalf("body = %q, want %q", got, test.wantMsg)
			}
			if client.listCalls != 0 {
				t.Fatalf("ListWorkflows called %d times for a rejected request", client.listCalls)
			}
		})
	}
}

// TestHandleListWorkflowsForwardsAcceptedFilters verifies the exact filter and
// cursor payload the handler forwards for accepted combinations.
func TestHandleListWorkflowsForwardsAcceptedFilters(t *testing.T) {
	tests := []struct {
		name        string
		target      string
		wantFilters *workflowspb.ListWorkflowsFilters
		wantCursor  string
	}{
		{
			name:   "no filters defaults to live workflows",
			target: "/workflows",
			wantFilters: &workflowspb.ListWorkflowsFilters{
				Query: "", Kind: "", BuildStatus: "", IsTerminated: false, IntervalMin: 0, IntervalMax: 0,
			},
			wantCursor: "",
		},
		{
			name:   "terminated true has no build status",
			target: "/workflows?terminated=true",
			wantFilters: &workflowspb.ListWorkflowsFilters{
				Query: "", Kind: "", BuildStatus: "", IsTerminated: true, IntervalMin: 0, IntervalMax: 0,
			},
			wantCursor: "",
		},
		{
			name:   "explicit terminated false is preserved",
			target: "/workflows?terminated=false",
			wantFilters: &workflowspb.ListWorkflowsFilters{
				Query: "", Kind: "", BuildStatus: "", IsTerminated: false, IntervalMin: 0, IntervalMax: 0,
			},
			wantCursor: "",
		},
		{
			name:   "build status with terminated false is accepted",
			target: "/workflows?build_status=FAILED&terminated=false",
			wantFilters: &workflowspb.ListWorkflowsFilters{
				Query: "", Kind: "", BuildStatus: "FAILED", IsTerminated: false, IntervalMin: 0, IntervalMax: 0,
			},
			wantCursor: "",
		},
		{
			name:   "every filter is forwarded",
			target: "/workflows?query=nightly&kind=HEARTBEAT&build_status=COMPLETED&interval_min=30&interval_max=90",
			wantFilters: &workflowspb.ListWorkflowsFilters{
				Query: "nightly", Kind: "HEARTBEAT", BuildStatus: "COMPLETED", IsTerminated: false, IntervalMin: 30, IntervalMax: 90,
			},
			wantCursor: "",
		},
		{
			name:   "equal interval bounds are accepted",
			target: "/workflows?interval_min=60&interval_max=60",
			wantFilters: &workflowspb.ListWorkflowsFilters{
				Query: "", Kind: "", BuildStatus: "", IsTerminated: false, IntervalMin: 60, IntervalMax: 60,
			},
			wantCursor: "",
		},
		{
			name:   "interval_max without interval_min is accepted",
			target: "/workflows?interval_max=120",
			wantFilters: &workflowspb.ListWorkflowsFilters{
				Query: "", Kind: "", BuildStatus: "", IsTerminated: false, IntervalMin: 0, IntervalMax: 120,
			},
			wantCursor: "",
		},
		{
			name:   "opaque cursor is forwarded verbatim",
			target: "/workflows?cursor=eyJvIjoyMH0%3D%3D",
			wantFilters: &workflowspb.ListWorkflowsFilters{
				Query: "", Kind: "", BuildStatus: "", IsTerminated: false, IntervalMin: 0, IntervalMax: 0,
			},
			wantCursor: "eyJvIjoyMH0==",
		},
		{
			name:   "empty cursor value behaves like an absent cursor",
			target: "/workflows?cursor=",
			wantFilters: &workflowspb.ListWorkflowsFilters{
				Query: "", Kind: "", BuildStatus: "", IsTerminated: false, IntervalMin: 0, IntervalMax: 0,
			},
			wantCursor: "",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &recordingWorkflowsClient{}
			s := &Server{workflowsClient: client}

			res := httptest.NewRecorder()
			newListWorkflowsRouter(s).ServeHTTP(res, newWorkflowsListRequest(test.target, "user-1"))

			if res.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d (body %q)", res.Code, http.StatusOK, res.Body.String())
			}
			if client.listCalls != 1 {
				t.Fatalf("ListWorkflows calls = %d, want 1", client.listCalls)
			}
			if got := client.listReq.GetUserId(); got != "user-1" {
				t.Fatalf("user_id = %q, want %q", got, "user-1")
			}
			if got := client.listReq.GetCursor(); got != test.wantCursor {
				t.Fatalf("cursor = %q, want %q", got, test.wantCursor)
			}
			assertWorkflowFilters(t, client.listReq.GetFilters(), test.wantFilters)
			if got := res.Header().Get("Content-Type"); got != "application/json" {
				t.Fatalf("Content-Type = %q, want application/json", got)
			}
		})
	}
}

func assertWorkflowFilters(t *testing.T, got, want *workflowspb.ListWorkflowsFilters) {
	t.Helper()

	if got.GetQuery() != want.GetQuery() {
		t.Fatalf("query = %q, want %q", got.GetQuery(), want.GetQuery())
	}
	if got.GetKind() != want.GetKind() {
		t.Fatalf("kind = %q, want %q", got.GetKind(), want.GetKind())
	}
	if got.GetBuildStatus() != want.GetBuildStatus() {
		t.Fatalf("build_status = %q, want %q", got.GetBuildStatus(), want.GetBuildStatus())
	}
	if got.GetIsTerminated() != want.GetIsTerminated() {
		t.Fatalf("is_terminated = %v, want %v", got.GetIsTerminated(), want.GetIsTerminated())
	}
	if got.GetIntervalMin() != want.GetIntervalMin() {
		t.Fatalf("interval_min = %d, want %d", got.GetIntervalMin(), want.GetIntervalMin())
	}
	if got.GetIntervalMax() != want.GetIntervalMax() {
		t.Fatalf("interval_max = %d, want %d", got.GetIntervalMax(), want.GetIntervalMax())
	}
}

// TestHandleListWorkflowsCursorRoundTrip verifies the pagination cursor the
// backend returns is the cursor the client can page with next.
func TestHandleListWorkflowsCursorRoundTrip(t *testing.T) {
	const nextCursor = "eyJvZmZzZXQiOjIwfQ=="

	client := &recordingWorkflowsClient{
		listResp: &workflowspb.ListWorkflowsResponse{Cursor: nextCursor},
	}
	s := &Server{workflowsClient: client}

	res := httptest.NewRecorder()
	newListWorkflowsRouter(s).ServeHTTP(res, newWorkflowsListRequest("/workflows", "user-1"))
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

	// Paging with that cursor must hand the identical value to the backend.
	next := httptest.NewRecorder()
	newListWorkflowsRouter(s).ServeHTTP(next, newWorkflowsListRequest("/workflows?cursor="+nextCursor, "user-1"))
	if next.Code != http.StatusOK {
		t.Fatalf("paged status = %d, want %d", next.Code, http.StatusOK)
	}
	if client.listCalls != 2 {
		t.Fatalf("ListWorkflows calls = %d, want 2", client.listCalls)
	}
	if got := client.listReq.GetCursor(); got != nextCursor {
		t.Fatalf("paged cursor = %q, want %q", got, nextCursor)
	}
}

// TestHandleListWorkflowsRequiresAuthenticatedUser proves the handler refuses
// to fan out to the backend when the session middleware did not populate a
// user, instead of listing another user's workflows.
func TestHandleListWorkflowsRequiresAuthenticatedUser(t *testing.T) {
	client := &recordingWorkflowsClient{}
	s := &Server{workflowsClient: client}

	res := httptest.NewRecorder()
	newListWorkflowsRouter(s).ServeHTTP(res, newWorkflowsListRequest("/workflows?kind=HEARTBEAT", ""))

	if res.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusBadRequest)
	}
	if got := res.Body.String(); got != "user ID not found\n" {
		t.Fatalf("body = %q, want %q", got, "user ID not found\n")
	}
	if client.listCalls != 0 {
		t.Fatalf("ListWorkflows called %d times without an authenticated user", client.listCalls)
	}
}

// TestHandleListWorkflowsBackendErrorHidesDetail verifies a failing backend
// produces a generic 500 and never leaks the internal gRPC message.
func TestHandleListWorkflowsBackendErrorHidesDetail(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		wantCode int
	}{
		{name: "not found", err: status.Error(codes.NotFound, "workflow not found"), wantCode: http.StatusNotFound},
		{name: "permission denied", err: status.Error(codes.PermissionDenied, "not your workflow"), wantCode: http.StatusForbidden},
		{name: "unavailable", err: status.Error(codes.Unavailable, "workflows-service down"), wantCode: http.StatusServiceUnavailable},
		{name: "internal", err: status.Error(codes.Internal, "pq: connection reset by peer"), wantCode: http.StatusInternalServerError},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &recordingWorkflowsClient{listErr: test.err}
			s := &Server{workflowsClient: client}

			res := httptest.NewRecorder()
			newListWorkflowsRouter(s).ServeHTTP(res, newWorkflowsListRequest("/workflows", "user-1"))

			if res.Code != test.wantCode {
				t.Fatalf("status = %d, want %d", res.Code, test.wantCode)
			}
			if got := res.Body.String(); got != "failed to list workflows\n" {
				t.Fatalf("body = %q, want %q", got, "failed to list workflows\n")
			}
		})
	}
}

// TestHandleListWorkflowsMissingFiltersStillSucceeds documents that the backend
// receives a non-nil filter message for an unfiltered request, which the
// service relies on to apply its own defaults.
func TestHandleListWorkflowsMissingFiltersStillSucceeds(t *testing.T) {
	client := &recordingWorkflowsClient{}
	s := &Server{workflowsClient: client}

	res := httptest.NewRecorder()
	newListWorkflowsRouter(s).ServeHTTP(res, newWorkflowsListRequest("/workflows", "user-1"))

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusOK)
	}
	if client.listReq.Filters == nil {
		t.Fatal("expected a non-nil filter message for an unfiltered request")
	}
}
