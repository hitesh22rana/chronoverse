package workflow

import (
	"context"
	"errors"
	"fmt"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	logsTable      = "job_logs"
	defaultTimeout = time.Second * 30
)

// deleteWorkflow completes the deletion of a workflow by removing all associated logs.
func (r *Repository) deleteWorkflow(parentCtx context.Context, workflowID, userID string) error {
	return r.deleteWorkflowLogs(parentCtx, workflowID, userID)
}

// deleteWorkflowLogs deletes all logs associated with the workflow.
func (r *Repository) deleteWorkflowLogs(parentCtx context.Context, workflowID, userID string) error {
	// This is non-blocking and runs in the background
	query := fmt.Sprintf(`
        ALTER TABLE %s DELETE
        WHERE workflow_id = $1 AND user_id = $2
    `, logsTable)

	return withRetry(func() error {
		ctx, cancel := context.WithTimeout(parentCtx, defaultTimeout)
		defer cancel()

		err := r.ch.Exec(ctx, query, workflowID, userID)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
				return status.Error(status.Code(err), err.Error())
			}

			return status.Errorf(codes.Internal, "failed to initiate async deletion of logs for workflow %s: %v", workflowID, err)
		}

		return nil
	})
}
