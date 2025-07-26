package workflow

import (
	"context"
	"errors"
	"fmt"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	workflowsmodel "github.com/hitesh22rana/chronoverse/internal/model/workflows"
	"github.com/hitesh22rana/chronoverse/internal/pkg/clickhouse"
)

const (
	deleteWorkflowExpirationTimeout = 1 * time.Minute
	defaultTimeout                  = time.Second * 30
)

// deleteWorkflow completes the deletion of a workflow by removing all associated logs.
func (r *Repository) deleteWorkflow(parentCtx context.Context, workflowID, userID string) error {
	// Acquire a distributed lock to ensure only one worker processes the job at a time
	lockKey := fmt.Sprintf(
		"%s:%s:%s",
		lockKeyPrefix,
		workflowsmodel.ActionDelete.ToString(),
		workflowID,
	)
	isLockAcquired, err := r.rdb.AcquireDistributedLock(parentCtx, lockKey, deleteWorkflowExpirationTimeout)
	if err != nil || !isLockAcquired {
		return status.Error(codes.Aborted, "failed to acquire distributed lock")
	}

	// Release the distributed lock
	defer func() {
		//nolint:errcheck // Ignore the error as we don't want to block the job execution, since, the lock might have been auto-released due to expiration
		_ = r.rdb.ReleaseDistributedLock(parentCtx, lockKey)
	}()

	return r.deleteWorkflowLogs(parentCtx, workflowID, userID)
}

// deleteWorkflowLogs deletes all logs associated with the workflow.
func (r *Repository) deleteWorkflowLogs(parentCtx context.Context, workflowID, userID string) error {
	// This is non-blocking and runs in the background
	query := fmt.Sprintf(`
        ALTER TABLE %s DELETE
        WHERE workflow_id = $1 AND user_id = $2
    `, clickhouse.TableJobLogs)

	return withRetry(func() error {
		ctx, cancel := context.WithTimeout(parentCtx, defaultTimeout)
		defer cancel()

		err := r.ch.Exec(ctx, query, workflowID, userID)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				err = status.Error(codes.DeadlineExceeded, err.Error())
				return err
			} else if errors.Is(err, context.Canceled) {
				err = status.Error(codes.Canceled, err.Error())
				return err
			}

			return status.Errorf(codes.Internal, "failed to initiate async deletion of logs for workflow %s: %v", workflowID, err)
		}

		return nil
	})
}
