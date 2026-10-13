//nolint:testpackage // Verifies guards without database access.
package jobs

import (
	"testing"

	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	authmock "github.com/hitesh22rana/chronoverse/internal/pkg/auth/mock"
	workflowspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/workflows"
)

func TestJobLogReadGuards(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, tc := range []struct {
			name                 string
			authErr, workflowErr error
			retention            bool
			cursor               string
			code                 codes.Code
			calls                int
		}{
			{name: "authorization", authErr: status.Error(codes.Unauthenticated, "token unavailable"), code: codes.Unauthenticated},
			{name: "workflow lookup", workflowErr: status.Error(codes.Unavailable, "workflow unavailable"), code: codes.Unavailable, calls: 1},
			{name: "retention disabled", code: codes.FailedPrecondition, calls: 1},
			{name: "malformed cursor", retention: true, cursor: "not-base64", code: codes.InvalidArgument, calls: 1},
		} {
			if stream && tc.cursor != "" {
				continue
			}
			prefix := "read/"
			if stream {
				prefix = "stream/"
			}
			t.Run(prefix+tc.name, func(t *testing.T) {
				issuer := authmock.NewMockIAuth(gomock.NewController(t))
				issuer.EXPECT().IssueToken(gomock.Any(), authSubject, "workflows-service").Return("token", tc.authErr)
				client := &logSearchWorkflowClient{response: &workflowspb.GetWorkflowResponse{LogRetention: tc.retention}, err: tc.workflowErr}
				repo := New(&Config{}, issuer, nil, nil, nil, nil, &Services{Workflows: client})
				if stream {
					sub, err := repo.StreamJobLogs(t.Context(), "job", "workflow", "user")
					if sub != nil || status.Code(err) != tc.code {
						t.Fatalf("subscription=%v error=%v, want %v", sub, err, tc.code)
					}
				} else {
					result, state, err := repo.GetJobLogs(t.Context(), "job", "workflow", "user", tc.cursor, jobsmodel.JobLogsSortOrderDesc, &jobsmodel.GetJobLogsFilters{})
					if result != nil || state != "" || status.Code(err) != tc.code {
						t.Fatalf("result=%v state=%q error=%v, want %v", result, state, err, tc.code)
					}
				}
				if client.calls != tc.calls {
					t.Fatalf("workflow calls=%d, want %d", client.calls, tc.calls)
				}
			})
		}
	}
}
