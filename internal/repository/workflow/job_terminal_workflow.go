package workflow

import (
	"context"
	"fmt"

	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	notificationsmodel "github.com/hitesh22rana/chronoverse/internal/model/notifications"
	workflowsmodel "github.com/hitesh22rana/chronoverse/internal/model/workflows"
	loggerpkg "github.com/hitesh22rana/chronoverse/internal/pkg/logger"
	workflowspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/workflows"
)

func (r *Repository) handleJobCompleted(ctx context.Context, workflowEvent *workflowsmodel.WorkflowEvent) error {
	authCtx, workflow, err := r.loadWorkflowForTerminalJobEvent(ctx, workflowEvent)
	if err != nil || workflow == nil {
		return err
	}

	if _, err = r.svc.Workflows.ResetWorkflowConsecutiveJobFailuresCount(authCtx, &workflowspb.ResetWorkflowConsecutiveJobFailuresCountRequest{
		Id:     workflow.GetId(),
		UserId: workflow.GetUserId(),
	}); err != nil {
		if status.Code(err) != codes.NotFound {
			return err
		}
	}

	return r.sendNotification(
		authCtx,
		workflow.GetUserId(),
		workflow.GetId(),
		workflowEvent.JobID,
		"Job Execution Completed",
		fmt.Sprintf("Job execution completed successfully for workflow '%s'.", workflow.GetName()),
		notificationsmodel.KindWebSuccess.ToString(),
		notificationsmodel.EntityJob.ToString(),
		"",
	)
}

func (r *Repository) handleJobFailed(ctx context.Context, workflowEvent *workflowsmodel.WorkflowEvent) error {
	authCtx, workflow, err := r.loadWorkflowForTerminalJobEvent(ctx, workflowEvent)
	if err != nil || workflow == nil {
		return err
	}

	res, err := r.svc.Workflows.IncrementWorkflowConsecutiveJobFailuresCount(authCtx, &workflowspb.IncrementWorkflowConsecutiveJobFailuresCountRequest{
		Id:     workflow.GetId(),
		UserId: workflow.GetUserId(),
		JobId:  workflowEvent.JobID,
	})
	thresholdReached := false
	if err != nil {
		if status.Code(err) != codes.NotFound {
			return err
		}
	} else {
		thresholdReached = res.GetThresholdReached()
	}

	if thresholdReached {
		if _, err = r.svc.Workflows.TerminateWorkflow(authCtx, &workflowspb.TerminateWorkflowRequest{
			Id:     workflow.GetId(),
			UserId: workflow.GetUserId(),
		}); err != nil {
			if status.Code(err) == codes.NotFound {
				return nil
			}
			return err
		}
	}

	if notifyErr := r.sendNotification(
		authCtx,
		workflow.GetUserId(),
		workflow.GetId(),
		workflowEvent.JobID,
		"Job Execution Failed",
		fmt.Sprintf("Job execution failed for workflow '%s'. Please check the logs for more details.", workflow.GetName()),
		notificationsmodel.KindWebError.ToString(),
		notificationsmodel.EntityJob.ToString(),
		"",
	); notifyErr != nil {
		return notifyErr
	}

	if !thresholdReached {
		return nil
	}

	return r.sendNotification(
		authCtx,
		workflow.GetUserId(),
		workflow.GetId(),
		workflowEvent.JobID,
		"Workflow Terminated",
		fmt.Sprintf("Workflow '%s' has been terminated after reaching %d consecutive job failures...", workflow.GetName(), workflow.GetMaxConsecutiveJobFailuresAllowed()),
		notificationsmodel.KindWebAlert.ToString(),
		notificationsmodel.EntityWorkflow.ToString(),
		workflowEvent.JobID,
	)
}

func (r *Repository) loadWorkflowForTerminalJobEvent(
	ctx context.Context,
	workflowEvent *workflowsmodel.WorkflowEvent,
) (context.Context, *workflowspb.GetWorkflowByIDResponse, error) {
	if workflowEvent.JobID == "" {
		return nil, nil, status.Error(codes.InvalidArgument, "missing job ID for terminal job event")
	}

	authCtx, err := r.withAuthorization(ctx)
	if err != nil {
		return nil, nil, err
	}

	workflow, err := r.svc.Workflows.GetWorkflowByID(authCtx, &workflowspb.GetWorkflowByIDRequest{
		Id: workflowEvent.ID,
	})
	if err != nil {
		if status.Code(err) == codes.NotFound {
			loggerpkg.FromContext(ctx).Warn("terminal job event references missing workflow",
				zap.String("workflow_id", workflowEvent.ID),
				zap.String("job_id", workflowEvent.JobID),
				zap.String("action", workflowEvent.Action.ToString()),
			)
			return authCtx, nil, nil
		}
		return nil, nil, err
	}

	return authCtx, workflow, nil
}
