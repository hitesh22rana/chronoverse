package workflow

import (
	"context"
	"slices"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	workflowsmodel "github.com/hitesh22rana/chronoverse/internal/model/workflows"
	jobspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"
	workflowspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/workflows"
)

func (r *Repository) cleanupContainerEvent(parentCtx context.Context, event *workflowsmodel.WorkflowEvent) error {
	snapshot := event.ContainerCleanup
	if snapshot == nil {
		return status.Error(codes.InvalidArgument, "container cleanup snapshot is required")
	}
	if slices.Contains([]string{snapshot.ContainerID, snapshot.RuntimeEndpoint, event.ID, event.UserID, event.JobID}, "") {
		return status.Error(codes.InvalidArgument, "container cleanup snapshot is incomplete")
	}
	job := &jobspb.JobsResponse{Id: event.JobID, ContainerId: snapshot.ContainerID, RuntimeNodeId: snapshot.RuntimeNodeID, RuntimeEndpoint: snapshot.RuntimeEndpoint, Attempts: snapshot.Attempts}
	workflow := &workflowspb.GetWorkflowByIDResponse{Id: event.ID, UserId: event.UserID, Kind: workflowsmodel.KindContainer.ToString(), LogRetention: snapshot.LogRetention}
	err := r.cleanupCanceledJobContainer(parentCtx, workflow, job)
	if err == nil {
		return nil
	}
	if status.Code(err) == codes.Canceled || status.Code(err) == codes.DeadlineExceeded {
		return err
	}
	if parentCtx.Err() != nil {
		return status.FromContextError(parentCtx.Err()).Err()
	}
	// A failed cleanup keeps the immutable Kafka intent retryable, even for adapter-specific codes.
	return status.Errorf(codes.Unavailable, "container cleanup for job %s: %v", event.JobID, err)
}
