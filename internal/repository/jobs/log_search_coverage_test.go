//nolint:testpackage // Covers repository guards before external I/O.
package jobs

import (
	"context"
	"testing"

	"go.uber.org/mock/gomock"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	authmock "github.com/hitesh22rana/chronoverse/internal/pkg/auth/mock"
	workflowspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/workflows"
)

type logSearchWorkflowClient struct {
	fakeWorkflowsService
	response *workflowspb.GetWorkflowResponse
	err      error
	calls    int
}

func (f *logSearchWorkflowClient) GetWorkflow(_ context.Context, _ *workflowspb.GetWorkflowRequest, _ ...grpc.CallOption) (*workflowspb.GetWorkflowResponse, error) {
	f.calls++
	return f.response, f.err
}

func TestSearchJobLogsGuards(t *testing.T) {
	const validID = "550e8400-e29b-41d4-a716-446655440000"
	cases := []struct {
		name                 string
		authErr, workflowErr error
		retention            bool
		jobID, cursor        string
		code                 codes.Code
		workflowCalls        int
	}{
		{name: "authorization failure", authErr: status.Error(codes.Unauthenticated, "token unavailable"), code: codes.Unauthenticated},
		{name: "workflow unavailable", workflowErr: status.Error(codes.Unavailable, "workflow unavailable"), code: codes.Unavailable, workflowCalls: 1},
		{name: "retention disabled", code: codes.FailedPrecondition, workflowCalls: 1},
		{name: "invalid job identity", retention: true, jobID: "bad\" OR user_id != \"tenant", code: codes.InvalidArgument, workflowCalls: 1},
		{name: "invalid cursor", retention: true, jobID: validID, cursor: "not-base64", code: codes.InvalidArgument, workflowCalls: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			issuer := authmock.NewMockIAuth(gomock.NewController(t))
			issuer.EXPECT().IssueToken(gomock.Any(), authSubject, "workflows-service").Return("token", tc.authErr)
			client := &logSearchWorkflowClient{response: &workflowspb.GetWorkflowResponse{LogRetention: tc.retention}, err: tc.workflowErr}
			repo := New(&Config{}, issuer, nil, nil, nil, nil, &Services{Workflows: client})
			result, jobStatus, err := repo.SearchJobLogs(t.Context(), tc.jobID, validID, validID, tc.cursor, &jobsmodel.SearchJobLogsFilters{}, jobsmodel.SearchJobLogsOptions{})
			if status.Code(err) != tc.code || result != nil || jobStatus != "" {
				t.Fatalf("result=%v status=%q error=%v, want %v", result, jobStatus, err, tc.code)
			}
			if client.calls != tc.workflowCalls {
				t.Fatalf("workflow calls=%d, want %d", client.calls, tc.workflowCalls)
			}
		})
	}
}
