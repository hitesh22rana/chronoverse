package workflow

import (
	"context"
	"errors"
	"slices"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	workflowsmodel "github.com/hitesh22rana/chronoverse/internal/model/workflows"
	loggerpkg "github.com/hitesh22rana/chronoverse/internal/pkg/logger"
	jobspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"
	workflowspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/workflows"
)

func (r *Repository) cleanupContainerEvent(parentCtx context.Context, event *workflowsmodel.WorkflowEvent) error {
	snapshot := event.ContainerCleanup
	if snapshot == nil {
		return status.Error(codes.InvalidArgument, "container cleanup snapshot is required")
	}
	if slices.Contains([]string{snapshot.ContainerID, snapshot.RuntimeEndpoint, snapshot.RuntimeInstance, snapshot.IntentID, event.ID, event.UserID, event.JobID}, "") {
		return status.Error(codes.InvalidArgument, "container cleanup snapshot is incomplete")
	}
	if event.CleanupGeneration <= 0 {
		return status.Error(codes.InvalidArgument, "cleanup generation is required")
	}
	attemptCtx, cancel := context.WithTimeout(parentCtx, 5*time.Second)
	authCtx, err := r.withAuthorization(attemptCtx)
	if err != nil {
		cancel()
		return cleanupRetryError(parentCtx, event, err)
	}
	request := &jobspb.ContainerCleanupAttemptRequest{
		JobId:           event.JobID,
		ContainerId:     snapshot.ContainerID,
		Generation:      event.CleanupGeneration,
		RuntimeInstance: snapshot.RuntimeInstance,
		IntentId:        snapshot.IntentID,
	}
	attempt, err := r.svc.Jobs.BeginContainerCleanupAttempt(authCtx, request)
	cancel()
	if err != nil {
		return cleanupRetryError(parentCtx, event, err)
	}
	switch attempt.GetState() {
	case "COMPLETED", "STALE":
		return nil
	case "QUARANTINED":
		logCleanupQuarantine(parentCtx, event, attempt)
		return nil
	case "PROCESSING":
		if attempt.GetToken() == "" {
			return status.Error(codes.Unavailable, "cleanup attempt has no ownership token")
		}
	default:
		return status.Error(codes.Unavailable, "cleanup attempt has no executable disposition")
	}
	request.Token = attempt.GetToken()
	job := &jobspb.JobsResponse{
		Id:              event.JobID,
		ContainerId:     snapshot.ContainerID,
		RuntimeNodeId:   snapshot.RuntimeNodeID,
		RuntimeEndpoint: snapshot.RuntimeEndpoint,
		Attempts:        snapshot.Attempts,
		RuntimeInstance: snapshot.RuntimeInstance,
		IntentId:        snapshot.IntentID,
	}
	workflow := &workflowspb.GetWorkflowByIDResponse{Id: event.ID, UserId: event.UserID, Kind: workflowsmodel.KindContainer.ToString(), LogRetention: snapshot.LogRetention}
	err = r.cleanupCanceledJobContainer(parentCtx, workflow, job)
	if err == nil {
		return nil
	}
	request.ErrorCode = status.Code(err).String()
	request.Interrupted = parentCtx.Err() != nil
	reportCtx, reportCancel := context.WithTimeout(context.WithoutCancel(parentCtx), 5*time.Second)
	defer reportCancel()
	reportAuth, reportErr := r.withAuthorization(reportCtx)
	if reportErr != nil {
		return cleanupRetryError(parentCtx, event, reportErr)
	}
	disposition, reportErr := r.svc.Jobs.FailContainerCleanupAttempt(reportAuth, request)
	if reportErr != nil {
		return cleanupRetryError(parentCtx, event, reportErr)
	}
	switch disposition.GetState() {
	case "COMPLETED", "STALE":
		return nil
	case "QUARANTINED":
		logCleanupQuarantine(parentCtx, event, disposition)
		return nil
	default:
		return cleanupRetryError(parentCtx, event, err)
	}
}

func cleanupRetryError(ctx context.Context, event *workflowsmodel.WorkflowEvent, err error) error {
	if ctx.Err() != nil {
		err = status.FromContextError(ctx.Err()).Err()
	} else if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		err = status.FromContextError(err).Err()
	}
	code := status.Code(err)
	if code != codes.Canceled && code != codes.DeadlineExceeded {
		code = codes.Unavailable
	}
	return status.Errorf(code, "container cleanup for job %s container %s: %v", event.JobID, event.ContainerCleanup.ContainerID, err)
}

func logCleanupQuarantine(ctx context.Context, event *workflowsmodel.WorkflowEvent, attempt *jobspb.ContainerCleanupAttemptResponse) {
	loggerpkg.FromContext(ctx).Error("container cleanup quarantined; administrative redrive required",
		zap.String("workflow_id", event.ID), zap.String("job_id", event.JobID), zap.String("container_id", event.ContainerCleanup.ContainerID),
		zap.Int64("cleanup_generation", attempt.GetGeneration()), zap.Int32("cleanup_attempts", attempt.GetAttempts()))
}
