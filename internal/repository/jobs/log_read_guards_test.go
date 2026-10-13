//nolint:testpackage // Verifies guards without database access.
package jobs

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	authmock "github.com/hitesh22rana/chronoverse/internal/pkg/auth/mock"
	"github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
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

// Exercise real PostgreSQL classifiers without opening a pool.
func TestMapJobLogsStatusError(t *testing.T) {
	repo := New(&Config{}, nil, &postgres.Postgres{}, nil, nil, nil, nil)

	invalidText := &pgconn.PgError{Code: "22P02", Message: `invalid input syntax for type uuid: "invalid"`}
	uniqueViolation := &pgconn.PgError{Code: "23505", Message: "duplicate key value violates unique constraint"}

	for _, tc := range []struct {
		name string
		err  error
		code codes.Code
		want string
	}{
		{
			name: "deadline exceeded",
			err:  fmt.Errorf("scan job status: %w", context.DeadlineExceeded),
			code: codes.DeadlineExceeded,
			want: context.DeadlineExceeded.Error(),
		},
		{
			name: "canceled",
			err:  fmt.Errorf("scan job status: %w", context.Canceled),
			code: codes.Canceled,
			want: context.Canceled.Error(),
		},
		{
			name: "no rows",
			err:  fmt.Errorf("read job status: %w", pgx.ErrNoRows),
			code: codes.NotFound,
			want: "job not found or not owned by user",
		},
		{
			name: "invalid text representation",
			err:  fmt.Errorf("read job status: %w", invalidText),
			code: codes.InvalidArgument,
			want: "invalid job ID",
		},
		{
			// A different SQLSTATE must fall through instead of being read as a
			// malformed identifier.
			name: "other pg error",
			err:  fmt.Errorf("read job status: %w", uniqueViolation),
			code: codes.Internal,
			want: "failed to get job",
		},
		{
			name: "generic failure",
			err:  errors.New("connection refused"),
			code: codes.Internal,
			want: "failed to get job",
		},
		{
			// Cancellation outranks a missing job: both errors can surface from the
			// same scan, and the caller needs the cancellation to stop work.
			name: "canceled outranks no rows",
			err:  errors.Join(pgx.ErrNoRows, context.Canceled),
			code: codes.Canceled,
			want: context.Canceled.Error(),
		},
		{
			name: "deadline exceeded outranks no rows",
			err:  errors.Join(pgx.ErrNoRows, context.DeadlineExceeded),
			code: codes.DeadlineExceeded,
			want: context.DeadlineExceeded.Error(),
		},
		{
			name: "no rows outranks invalid text",
			err:  errors.Join(invalidText, pgx.ErrNoRows),
			code: codes.NotFound,
			want: "job not found or not owned by user",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := repo.mapJobLogsStatusError(tc.err)
			if got := status.Code(err); got != tc.code {
				t.Fatalf("code=%v error=%v, want %v", got, err, tc.code)
			}
			if msg := status.Convert(err).Message(); !strings.Contains(msg, tc.want) {
				t.Fatalf("message=%q, want it to contain %q", msg, tc.want)
			}
		})
	}
}
