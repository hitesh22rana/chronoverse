package workflow

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/meilisearch/meilisearch-go"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	workflowsmodel "github.com/hitesh22rana/chronoverse/internal/model/workflows"
	"github.com/hitesh22rana/chronoverse/internal/pkg/clickhouse"
	meilisearchpkg "github.com/hitesh22rana/chronoverse/internal/pkg/meilisearch"
	retrypkg "github.com/hitesh22rana/chronoverse/internal/pkg/retry"
)

const (
	deleteWorkflowExpirationTimeout     = 15 * time.Minute
	deleteWorkflowOperationTimeout      = 2 * time.Minute
	deleteWorkflowMeiliSearchTimeout    = 2 * time.Minute
	deleteWorkflowMeiliSearchPollPeriod = 2 * time.Second
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
	if err := r.deleteWorkflowLogsFromClickHouse(parentCtx, workflowID, userID); err != nil {
		return err
	}

	return r.deleteWorkflowLogsFromMeiliSearch(parentCtx, workflowID, userID)
}

func (r *Repository) deleteWorkflowLogsFromClickHouse(parentCtx context.Context, workflowID, userID string) error {
	// mutations_sync waits for ClickHouse to finish the delete mutation before the Kafka record is committed.
	query := deleteWorkflowLogsClickHouseQuery()

	return retrypkg.Do(parentCtx, 2, retryBackoff, func() error {
		ctx, cancel := context.WithTimeout(parentCtx, deleteWorkflowOperationTimeout)
		defer cancel()

		err := r.ch.Exec(ctx, query, workflowID, userID)
		if err != nil {
			return deleteWorkflowStatusError(
				fmt.Sprintf("failed to delete ClickHouse logs for workflow %s", workflowID),
				err,
			)
		}

		return nil
	})
}

func deleteWorkflowLogsClickHouseQuery() string {
	return fmt.Sprintf(`
        ALTER TABLE %s DELETE
        WHERE workflow_id = ? AND user_id = ?
        SETTINGS mutations_sync = 2
    `, clickhouse.TableJobLogs)
}

func (r *Repository) deleteWorkflowLogsFromMeiliSearch(parentCtx context.Context, workflowID, userID string) error {
	if r.ms == nil {
		return status.Errorf(codes.Internal, "failed to delete Meilisearch logs for workflow %s: client is not configured", workflowID)
	}

	index := meilisearchpkg.Indexes[meilisearchpkg.IndexJobLogs]
	if index == nil {
		return status.Errorf(codes.Internal, "failed to delete Meilisearch logs for workflow %s: job logs index is not configured", workflowID)
	}

	filter := fmt.Sprintf("workflow_id = %q AND user_id = %q", workflowID, userID)
	var taskUID int64

	if err := retrypkg.Do(parentCtx, 2, retryBackoff, func() error {
		ctx, cancel := context.WithTimeout(parentCtx, deleteWorkflowOperationTimeout)
		defer cancel()

		taskInfo, err := r.ms.Index(index.Name).DeleteDocumentsByFilterWithContext(ctx, filter)
		if err != nil {
			return deleteWorkflowStatusError(
				fmt.Sprintf("failed to enqueue Meilisearch logs deletion for workflow %s", workflowID),
				err,
			)
		}
		if taskInfo == nil {
			return status.Errorf(codes.Internal, "failed to enqueue Meilisearch logs deletion for workflow %s: empty task response", workflowID)
		}

		taskUID = taskInfo.TaskUID
		return nil
	}); err != nil {
		return err
	}

	return r.waitForMeiliSearchDeleteTask(parentCtx, workflowID, taskUID)
}

func (r *Repository) waitForMeiliSearchDeleteTask(parentCtx context.Context, workflowID string, taskUID int64) error {
	return retrypkg.Do(parentCtx, 2, retryBackoff, func() error {
		ctx, cancel := context.WithTimeout(parentCtx, deleteWorkflowMeiliSearchTimeout)
		defer cancel()

		task, err := r.ms.WaitForTaskWithContext(ctx, taskUID, deleteWorkflowMeiliSearchPollPeriod)
		if err != nil {
			return deleteWorkflowStatusError(
				fmt.Sprintf("failed waiting for Meilisearch logs deletion task %d for workflow %s", taskUID, workflowID),
				err,
			)
		}

		if task.Status != meilisearch.TaskStatusSucceeded {
			return status.Errorf(
				codes.Internal,
				"Meilisearch logs deletion task %d for workflow %s finished with status %s: %v",
				taskUID,
				workflowID,
				task.Status,
				task.Error,
			)
		}

		return nil
	})
}

func deleteWorkflowStatusError(message string, err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return status.Error(codes.DeadlineExceeded, err.Error())
	}
	if errors.Is(err, context.Canceled) {
		return status.Error(codes.Canceled, err.Error())
	}

	return status.Errorf(codes.Internal, "%s: %v", message, err)
}
