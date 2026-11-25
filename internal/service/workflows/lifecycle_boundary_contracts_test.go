package workflows_test

import (
	"testing"

	"github.com/go-playground/validator/v10"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	servicepkg "github.com/hitesh22rana/chronoverse/internal/service/workflows"
	mocks "github.com/hitesh22rana/chronoverse/internal/service/workflows/mock"
	pb "github.com/hitesh22rana/chronoverse/pkg/proto/go/workflows"
)

func TestWorkflowPayloadAndBuildValidationPrecedesRepository(t *testing.T) {
	t.Parallel()
	service := servicepkg.New(validator.New(), mocks.NewMockRepository(gomock.NewController(t)))
	for _, kind := range []string{"HEARTBEAT", "CONTAINER", "UNKNOWN"} {
		result, err := service.CreateWorkflow(t.Context(), &pb.CreateWorkflowRequest{
			UserId: "user", Name: "workflow", Payload: `{}`, Kind: kind, Interval: 60,
			MaxConsecutiveJobFailuresAllowed: 3, IdempotencyKey: "create",
		})
		require.Empty(t, result)
		require.Equal(t, codes.InvalidArgument, status.Code(err))
	}
	err := service.UpdateWorkflow(t.Context(), &pb.UpdateWorkflowRequest{
		Id: "workflow", UserId: "user", Name: "updated", Payload: `{}`, Interval: 60,
		MaxConsecutiveJobFailuresAllowed: 3, IdempotencyKey: "update",
	})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	err = service.UpdateWorkflowBuildStatus(t.Context(), &pb.UpdateWorkflowBuildStatusRequest{
		Id: "workflow", UserId: "user", BuildStatus: "INVALID", Generation: 1,
	})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}
