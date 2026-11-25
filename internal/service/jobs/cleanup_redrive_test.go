package jobs_test

import (
	"context"
	"strings"
	"testing"

	"github.com/go-playground/validator/v10"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	"github.com/hitesh22rana/chronoverse/internal/pkg/auth"
	jobsservice "github.com/hitesh22rana/chronoverse/internal/service/jobs"
	jobsmock "github.com/hitesh22rana/chronoverse/internal/service/jobs/mock"
	jobspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"
)

func TestCleanupRedriveRequiresAuthenticatedActorAndRepairReason(t *testing.T) {
	for _, tc := range []struct {
		name, role, actor, reason string
		want                      codes.Code
	}{
		{"external", "", "operations/alice", "restored", codes.PermissionDenied},
		{"missing subject", "admin", "", "restored", codes.FailedPrecondition},
		{"empty reason", "admin", "operations/alice", "", codes.InvalidArgument},
		{"blank reason", "admin", "operations/alice", " \n ", codes.InvalidArgument},
		{"oversized reason", "admin", "operations/alice", strings.Repeat("é", 1025), codes.InvalidArgument},
		{"oversized actor", "admin", strings.Repeat("a", 257), "restored", codes.InvalidArgument},
		{"operator", "admin", "operations/alice", "  original daemon restored  ", codes.OK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := jobsmock.NewMockRepository(gomock.NewController(t))
			service := jobsservice.New(validator.New(), repo)
			ctx := auth.WithRole(t.Context(), tc.role)
			if tc.actor != "" {
				ctx = auth.WithSubject(ctx, tc.actor)
			}
			ctx = metadata.NewIncomingContext(ctx, metadata.Pairs("actor", "forged", "sub", "forged"))
			if tc.want == codes.OK {
				repo.EXPECT().UpdateContainerCleanup(gomock.Any(), jobsmodel.CleanupOperationRedrive, gomock.Any(), int64(3), "", "", false, jobsmodel.CleanupRedrive{
					Actor:  "operations/alice",
					Reason: "original daemon restored",
				}).
					DoAndReturn(func(_ context.Context, _ string, _ jobsmodel.CleanupIdentity, _ int64, _, _ string, _ bool, _ jobsmodel.CleanupRedrive) (*jobsmodel.CleanupAttempt, error) {
						return &jobsmodel.CleanupAttempt{State: "READY", Generation: 4}, nil
					})
			}
			_, err := service.RedriveContainerCleanup(ctx, &jobspb.ContainerCleanupAttemptRequest{
				JobId: intentContractID, ContainerId: strings.Repeat("a", 64), RuntimeInstance: "original", IntentId: intentContractID, Generation: 3, Reason: tc.reason,
			})
			if status.Code(err) != tc.want {
				t.Fatalf("redrive error=%v want=%v", err, tc.want)
			}
		})
	}
}
