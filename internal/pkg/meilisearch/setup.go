package meilisearch

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/meilisearch/meilisearch-go"
)

const (
	taskTimeout     = 1 * time.Minute
	pollingDuration = 2 * time.Second
)

// SetupIndexes creates every application index and configures its searchable,
// filterable and sortable attributes, waiting for each batch of async tasks to
// complete. It is safe to call repeatedly; indexes that already exist are left
// untouched.
func SetupIndexes(ctx context.Context, client meilisearch.ServiceManager) error {
	createTaskIDs := make([]int64, 0, len(Indexes))
	for _, index := range Indexes {
		createTask, err := client.CreateIndexWithContext(ctx, &meilisearch.IndexConfig{
			Uid:        index.Name,
			PrimaryKey: index.PrimaryKey,
		})
		if err != nil {
			return fmt.Errorf("create index %s: %w", index.Name, err)
		}
		createTaskIDs = append(createTaskIDs, createTask.TaskUID)
	}

	if err := waitForTasks(ctx, client, createTaskIDs); err != nil {
		return err
	}

	var updateTaskIDs []int64
	for _, index := range Indexes {
		meiliIndex := client.Index(index.Name)

		task, err := meiliIndex.UpdateSearchableAttributesWithContext(ctx, &index.Searchable)
		if err != nil {
			return fmt.Errorf("update searchable attributes of %s: %w", index.Name, err)
		}
		updateTaskIDs = append(updateTaskIDs, task.TaskUID)

		task, err = meiliIndex.UpdateFilterableAttributesWithContext(ctx, &index.Filterable)
		if err != nil {
			return fmt.Errorf("update filterable attributes of %s: %w", index.Name, err)
		}
		updateTaskIDs = append(updateTaskIDs, task.TaskUID)

		task, err = meiliIndex.UpdateSortableAttributesWithContext(ctx, &index.Sortable)
		if err != nil {
			return fmt.Errorf("update sortable attributes of %s: %w", index.Name, err)
		}
		updateTaskIDs = append(updateTaskIDs, task.TaskUID)
	}

	return waitForTasks(ctx, client, updateTaskIDs)
}

// waitForTasks polls the given async tasks until they all succeed.
func waitForTasks(ctx context.Context, client meilisearch.ServiceManager, taskIDs []int64) error {
	if len(taskIDs) == 0 {
		return nil
	}

	ticker := time.NewTicker(pollingDuration)
	defer ticker.Stop()

	timeout := time.After(taskTimeout)
	for {
		select {
		case <-timeout:
			return fmt.Errorf("timeout waiting for Meilisearch tasks: %v", taskIDs)
		case <-ctx.Done():
			return fmt.Errorf("wait for Meilisearch tasks: %w", ctx.Err())
		case <-ticker.C:
			tasks, err := client.GetTasksWithContext(ctx, &meilisearch.TasksQuery{
				UIDS:  taskIDs,
				Limit: int64(len(taskIDs)),
			})
			if err != nil {
				return fmt.Errorf("get Meilisearch task info: %w", err)
			}

			done := 0
			for i := range tasks.Results {
				task := &tasks.Results[i]
				//nolint:exhaustive // Non-terminal task statuses keep polling.
				switch task.Status {
				case "succeeded":
					done++
					continue
				case "failed":
					// Re-running against an already-provisioned database is a
					// no-op, so treat "already exists" as success for this
					// task and keep waiting for the remaining ones.
					if strings.Contains(task.Error.Code, "index_already_exists") {
						done++
						continue
					}
					return fmt.Errorf("meilisearch task %d failed: %v", task.UID, task.Error)
				case "canceled":
					return fmt.Errorf("meilisearch task %d was canceled", task.UID)
				}
			}

			if done == len(taskIDs) {
				return nil
			}
		}
	}
}
