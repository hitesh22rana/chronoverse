package jobs_test

import (
	"context"
	"testing"

	"github.com/go-playground/validator/v10"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	"github.com/hitesh22rana/chronoverse/internal/pkg/auth"
	jobsservice "github.com/hitesh22rana/chronoverse/internal/service/jobs"
	jobsmock "github.com/hitesh22rana/chronoverse/internal/service/jobs/mock"
	jobspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"
)

func TestContainerIntentAdmissionRequiresExecutionPrincipal(t *testing.T) {
	for _, tc := range []struct {
		name, role, subject string
		generation          int64
		want                codes.Code
	}{
		{"public", "", "", 1, codes.PermissionDenied},
		{"workflow cannot mint", auth.RoleAdmin.String(), "internal/workflow", 1, codes.PermissionDenied},
		{"generation missing", auth.RoleAdmin.String(), "internal/executor", 0, codes.InvalidArgument},
		{"executor", auth.RoleAdmin.String(), "internal/executor", 1, codes.OK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := jobsmock.NewMockRepository(gomock.NewController(t))
			service := jobsservice.New(validator.New(), repo)
			ctx := auth.WithSubject(auth.WithRole(t.Context(), tc.role), tc.subject)
			if tc.want == codes.OK {
				repo.EXPECT().UpdateContainerIntent(gomock.Any(), jobsmodel.ContainerIntentOperationEnsure, gomock.Any()).
					DoAndReturn(func(_ context.Context, _ string, cmd *jobsmodel.ContainerIntentCommand) (*jobsmodel.ContainerIntentResult, error) {
						if cmd.WorkflowGeneration != 1 || cmd.RuntimeInstance != "daemon-original" || cmd.LeaseToken != "owned-lease" {
							t.Fatalf("lost authority evidence: %+v", cmd)
						}
						return &jobsmodel.ContainerIntentResult{}, nil
					})
			}
			_, err := service.EnsureContainerIntent(ctx, &jobspb.ContainerIntentCommand{
				JobId: "00000000-0000-4000-8000-000000000015", LeaseToken: "owned-lease", Attempt: 1,
				WorkflowGeneration: tc.generation, RuntimeInstance: "daemon-original", RequestHash: "frozen",
			})
			if status.Code(err) != tc.want {
				t.Fatalf("admission error %v want %s", err, tc.want)
			}
		})
	}
}

func TestContainerIntentRedriveAuditsValidatedPrincipal(t *testing.T) {
	repo := jobsmock.NewMockRepository(gomock.NewController(t))
	service := jobsservice.New(validator.New(), repo)
	ctx := auth.WithSubject(auth.WithRole(t.Context(), auth.RoleAdmin.String()), "operations/alice")
	repo.EXPECT().UpdateContainerIntent(gomock.Any(), jobsmodel.ContainerIntentOperationRedrive, gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, cmd *jobsmodel.ContainerIntentCommand) (*jobsmodel.ContainerIntentResult, error) {
			if cmd.Actor != "operations/alice" || cmd.Generation != 3 || cmd.Reason != "original runtime restored" {
				t.Fatalf("untrusted/lost redrive evidence: %+v", cmd)
			}
			return &jobsmodel.ContainerIntentResult{}, nil
		})
	_, err := service.RedriveContainerIntent(ctx, &jobspb.ContainerIntentCommand{IntentId: "00000000-0000-4000-8000-000000000015", Generation: 3, Reason: "original runtime restored"})
	if err != nil {
		t.Fatal(err)
	}
}
