//nolint:testpackage // Drives the private delete path against real Redis, ClickHouse and Meilisearch.
package workflow

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/meilisearch/meilisearch-go"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	workflowsmodel "github.com/hitesh22rana/chronoverse/internal/model/workflows"
	"github.com/hitesh22rana/chronoverse/internal/pkg/clickhouse"
	meilisearchpkg "github.com/hitesh22rana/chronoverse/internal/pkg/meilisearch"
	"github.com/hitesh22rana/chronoverse/internal/pkg/testkit"
)

// deleteWorkflowTestRepository wires the delete path to the shared Redis,
// ClickHouse and Meilisearch containers.
func deleteWorkflowTestRepository(t *testing.T) *Repository {
	t.Helper()

	return &Repository{
		rdb: testkit.Redis(t),
		ch:  testkit.ClickHouse(t),
		ms:  testkit.Meilisearch(t),
	}
}

// deleteWorkflowSeedClickHouseLog inserts one job log row with the tenant ids
// the delete query filters on.
func deleteWorkflowSeedClickHouseLog(
	ctx context.Context,
	t *testing.T,
	ch *clickhouse.Client,
	eventID, userID, workflowID, jobID string,
	sequence uint32,
) {
	t.Helper()

	if err := ch.Exec(ctx, `
		INSERT INTO job_logs (event_id, user_id, workflow_id, job_id, timestamp, message, sequence_num, stream)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, eventID, userID, workflowID, jobID, time.Now().UTC(), "message "+eventID, sequence, "stdout"); err != nil {
		t.Fatalf("seed clickhouse log %q: %v", eventID, err)
	}
}

// deleteWorkflowClickHouseLogCount returns the number of job logs stored for
// the exact tenant pair.
func deleteWorkflowClickHouseLogCount(
	ctx context.Context,
	t *testing.T,
	ch *clickhouse.Client,
	workflowID, userID string,
) int {
	t.Helper()

	rows, err := ch.Query(ctx, `SELECT count(*) FROM job_logs WHERE workflow_id = ? AND user_id = ?`, workflowID, userID)
	if err != nil {
		t.Fatalf("count clickhouse logs: %v", err)
	}
	defer rows.Close()

	// ClickHouse count() is UInt64; the driver rejects scanning it into int.
	var count uint64
	if rows.Next() {
		if scanErr := rows.Scan(&count); scanErr != nil {
			t.Fatalf("scan clickhouse log count: %v", scanErr)
		}
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		t.Fatalf("read clickhouse log count: %v", rowsErr)
	}

	return int(count)
}

// deleteWorkflowSeedMeiliLog indexes one job log document and waits until the
// indexing task succeeds so a later search observes it deterministically.
func deleteWorkflowSeedMeiliLog(
	ctx context.Context,
	t *testing.T,
	ms meilisearch.ServiceManager,
	docID, userID, workflowID, jobID string,
) {
	t.Helper()

	task, err := ms.Index(meilisearchpkg.IndexJobLogs).AddDocumentsWithContext(ctx, []map[string]any{{
		"id":           docID,
		"event_id":     docID,
		"user_id":      userID,
		"workflow_id":  workflowID,
		"job_id":       jobID,
		"message":      "message " + docID,
		"sequence_num": 1,
		"stream":       "stdout",
		"timestamp":    time.Now().UTC().Format(time.RFC3339Nano),
	}}, nil)
	if err != nil {
		t.Fatalf("seed meilisearch document %q: %v", docID, err)
	}
	info, err := ms.WaitForTaskWithContext(ctx, task.TaskUID, 100*time.Millisecond)
	if err != nil || info.Status != meilisearch.TaskStatusSucceeded {
		t.Fatalf("wait for meilisearch seed task %d: info=%+v error=%v", task.TaskUID, info, err)
	}
}

// deleteWorkflowMeiliLogCount returns the number of indexed job logs matching
// the exact tenant pair through the same filter the delete path uses.
func deleteWorkflowMeiliLogCount(
	ctx context.Context,
	t *testing.T,
	ms meilisearch.ServiceManager,
	workflowID, userID string,
) int {
	t.Helper()

	result, err := ms.Index(meilisearchpkg.IndexJobLogs).SearchWithContext(ctx, "", &meilisearch.SearchRequest{
		Filter: fmt.Sprintf("workflow_id = %q AND user_id = %q", workflowID, userID),
		Limit:  100,
	})
	if err != nil {
		t.Fatalf("search meilisearch logs: %v", err)
	}

	return len(result.Hits)
}

// deleteWorkflowAssertLockFree proves the delete path released the distributed
// lock: the key can be acquired, and the probe releases it again immediately.
func deleteWorkflowAssertLockFree(ctx context.Context, t *testing.T, lock workflowLockStore, key string) {
	t.Helper()

	acquired, err := lock.AcquireDistributedLock(ctx, key, time.Minute)
	if err != nil {
		t.Fatalf("probe lock %q: %v", key, err)
	}
	if !acquired {
		t.Fatalf("lock %q is still held after the delete event", key)
	}
	if releaseErr := lock.ReleaseDistributedLock(ctx, key); releaseErr != nil {
		t.Fatalf("release probe lock %q: %v", key, releaseErr)
	}
}

func TestIntegrationDeleteWorkflowActionDeletesOnlyTenantLogs(t *testing.T) {
	ctx := context.Background()
	repo := deleteWorkflowTestRepository(t)

	ownerID, otherUserID := uuid.NewString(), uuid.NewString()
	workflowID, otherWorkflowID := uuid.NewString(), uuid.NewString()
	jobID := uuid.NewString()

	deleteWorkflowSeedClickHouseLog(ctx, t, repo.ch, "delete-owner-1", ownerID, workflowID, jobID, 1)
	deleteWorkflowSeedClickHouseLog(ctx, t, repo.ch, "delete-owner-2", ownerID, workflowID, jobID, 2)
	deleteWorkflowSeedClickHouseLog(ctx, t, repo.ch, "delete-other-user", otherUserID, workflowID, jobID, 1)
	deleteWorkflowSeedClickHouseLog(ctx, t, repo.ch, "delete-other-workflow", ownerID, otherWorkflowID, jobID, 1)

	deleteWorkflowSeedMeiliLog(ctx, t, repo.ms, "delete-doc-owner", ownerID, workflowID, jobID)
	deleteWorkflowSeedMeiliLog(ctx, t, repo.ms, "delete-doc-other-user", otherUserID, workflowID, jobID)
	deleteWorkflowSeedMeiliLog(ctx, t, repo.ms, "delete-doc-other-workflow", ownerID, otherWorkflowID, jobID)

	if got := deleteWorkflowClickHouseLogCount(ctx, t, repo.ch, workflowID, ownerID); got != 2 {
		t.Fatalf("owner clickhouse logs before delete = %d, want 2", got)
	}
	if got := deleteWorkflowMeiliLogCount(ctx, t, repo.ms, workflowID, ownerID); got != 1 {
		t.Fatalf("owner meilisearch logs before delete = %d, want 1", got)
	}

	lockKey := "workflow:DELETE:" + workflowID
	deleteWorkflowAssertLockFree(ctx, t, repo.rdb, lockKey)

	deleteEvent := &workflowsmodel.WorkflowEvent{
		ID:     workflowID,
		UserID: ownerID,
		Action: workflowsmodel.ActionDelete,
	}
	if err := repo.runTargetWorkflow(ctx, deleteEvent); err != nil {
		t.Fatalf("ActionDelete dispatch: %v", err)
	}

	if got := deleteWorkflowClickHouseLogCount(ctx, t, repo.ch, workflowID, ownerID); got != 0 {
		t.Fatalf("owner clickhouse logs after delete = %d, want 0", got)
	}
	if got := deleteWorkflowMeiliLogCount(ctx, t, repo.ms, workflowID, ownerID); got != 0 {
		t.Fatalf("owner meilisearch logs after delete = %d, want 0", got)
	}
	if got := deleteWorkflowClickHouseLogCount(ctx, t, repo.ch, workflowID, otherUserID); got != 1 {
		t.Fatalf("other-user clickhouse logs = %d, want 1 (tenant isolation)", got)
	}
	if got := deleteWorkflowMeiliLogCount(ctx, t, repo.ms, workflowID, otherUserID); got != 1 {
		t.Fatalf("other-user meilisearch logs = %d, want 1 (tenant isolation)", got)
	}
	if got := deleteWorkflowClickHouseLogCount(ctx, t, repo.ch, otherWorkflowID, ownerID); got != 1 {
		t.Fatalf("other-workflow clickhouse logs = %d, want 1 (tenant isolation)", got)
	}
	if got := deleteWorkflowMeiliLogCount(ctx, t, repo.ms, otherWorkflowID, ownerID); got != 1 {
		t.Fatalf("other-workflow meilisearch logs = %d, want 1 (tenant isolation)", got)
	}

	// The success path released the lock: an immediate redelivery can acquire
	// it again and completes as a no-op with the other tenants untouched.
	deleteWorkflowAssertLockFree(ctx, t, repo.rdb, lockKey)
	if err := repo.runTargetWorkflow(ctx, deleteEvent); err != nil {
		t.Fatalf("ActionDelete redelivery: %v", err)
	}
	if got := deleteWorkflowClickHouseLogCount(ctx, t, repo.ch, workflowID, otherUserID); got != 1 {
		t.Fatalf("other-user clickhouse logs after redelivery = %d, want 1", got)
	}
	if got := deleteWorkflowMeiliLogCount(ctx, t, repo.ms, otherWorkflowID, ownerID); got != 1 {
		t.Fatalf("other-workflow meilisearch logs after redelivery = %d, want 1", got)
	}
	deleteWorkflowAssertLockFree(ctx, t, repo.rdb, lockKey)
}

func TestIntegrationDeleteWorkflowActionLockContentionPreservesLogs(t *testing.T) {
	ctx := context.Background()
	repo := deleteWorkflowTestRepository(t)

	ownerID, workflowID := uuid.NewString(), uuid.NewString()
	jobID := uuid.NewString()
	deleteWorkflowSeedClickHouseLog(ctx, t, repo.ch, "contention-log", ownerID, workflowID, jobID, 1)
	deleteWorkflowSeedMeiliLog(ctx, t, repo.ms, "contention-doc", ownerID, workflowID, jobID)

	lockKey := "workflow:DELETE:" + workflowID
	acquired, err := repo.rdb.AcquireDistributedLock(ctx, lockKey, time.Minute)
	if err != nil || !acquired {
		t.Fatalf("pre-acquire lock %q: acquired=%v error=%v", lockKey, acquired, err)
	}

	deleteEvent := &workflowsmodel.WorkflowEvent{
		ID:     workflowID,
		UserID: ownerID,
		Action: workflowsmodel.ActionDelete,
	}
	err = repo.runTargetWorkflow(ctx, deleteEvent)
	if status.Code(err) != codes.Aborted || !strings.Contains(err.Error(), "failed to acquire distributed lock") {
		t.Fatalf("ActionDelete under contention = %v, want Aborted distributed lock error", err)
	}

	if got := deleteWorkflowClickHouseLogCount(ctx, t, repo.ch, workflowID, ownerID); got != 1 {
		t.Fatalf("clickhouse logs after rejected delete = %d, want 1", got)
	}
	if got := deleteWorkflowMeiliLogCount(ctx, t, repo.ms, workflowID, ownerID); got != 1 {
		t.Fatalf("meilisearch logs after rejected delete = %d, want 1", got)
	}

	if releaseErr := repo.rdb.ReleaseDistributedLock(ctx, lockKey); releaseErr != nil {
		t.Fatalf("release contention lock: %v", releaseErr)
	}
	if err := repo.runTargetWorkflow(ctx, deleteEvent); err != nil {
		t.Fatalf("ActionDelete after contention release: %v", err)
	}
	if got := deleteWorkflowClickHouseLogCount(ctx, t, repo.ch, workflowID, ownerID); got != 0 {
		t.Fatalf("clickhouse logs after retried delete = %d, want 0", got)
	}
	if got := deleteWorkflowMeiliLogCount(ctx, t, repo.ms, workflowID, ownerID); got != 0 {
		t.Fatalf("meilisearch logs after retried delete = %d, want 0", got)
	}
	deleteWorkflowAssertLockFree(ctx, t, repo.rdb, lockKey)
}

func TestIntegrationDeleteWorkflowActionMeiliEnqueueFailureReleasesLock(t *testing.T) {
	ctx := context.Background()

	fakeMS := &deleteWorkflowMeiliFake{
		enqueue: func(context.Context, any) (*meilisearch.TaskInfo, error) {
			return nil, errors.New("meilisearch enqueue failed")
		},
		wait: func(context.Context, int64) (*meilisearch.Task, error) {
			return nil, errors.New("wait must not be called after a failed enqueue")
		},
	}
	repo := &Repository{
		rdb: testkit.Redis(t),
		ch:  testkit.ClickHouse(t),
		ms:  fakeMS,
	}

	ownerID, workflowID := uuid.NewString(), uuid.NewString()
	jobID := uuid.NewString()
	deleteWorkflowSeedClickHouseLog(ctx, t, repo.ch, "meili-failure-log", ownerID, workflowID, jobID, 1)

	err := repo.runTargetWorkflow(ctx, &workflowsmodel.WorkflowEvent{
		ID:     workflowID,
		UserID: ownerID,
		Action: workflowsmodel.ActionDelete,
	})
	if status.Code(err) != codes.Internal || !strings.Contains(err.Error(), "failed to enqueue Meilisearch logs deletion") {
		t.Fatalf("ActionDelete error = %v, want Internal enqueue failure", err)
	}
	if len(fakeMS.enqueueCalls) != 2 {
		t.Fatalf("enqueue attempts = %d, want 2 (retried fixture)", len(fakeMS.enqueueCalls))
	}
	if len(fakeMS.waitCalls) != 0 {
		t.Fatalf("wait calls = %d, want 0 after a failed enqueue", len(fakeMS.waitCalls))
	}
	if got := deleteWorkflowClickHouseLogCount(ctx, t, repo.ch, workflowID, ownerID); got != 0 {
		t.Fatalf("clickhouse logs after enqueue failure = %d, want 0 (ClickHouse delete commits first)", got)
	}

	deleteWorkflowAssertLockFree(ctx, t, repo.rdb, "workflow:DELETE:"+workflowID)
}

func TestIntegrationDeleteWorkflowLogsClickHouseFailureSkipsMeiliSearch(t *testing.T) {
	ctx := context.Background()

	isolatedCH, err := clickhouse.New(ctx, &clickhouse.Config{
		Hosts: []string{testkit.ClickHouseAddr(t)}, Database: "chronoverse", Username: "chronoverse-client",
		Password: "chronoverse", TLSConfig: &clickhouse.TLSConfig{},
	})
	if err != nil {
		t.Fatalf("open isolated clickhouse client: %v", err)
	}
	if closeErr := isolatedCH.Close(); closeErr != nil {
		t.Fatalf("close isolated clickhouse client: %v", closeErr)
	}

	fakeMS := &deleteWorkflowMeiliFake{
		enqueue: func(context.Context, any) (*meilisearch.TaskInfo, error) {
			return nil, errors.New("enqueue must not be reached when ClickHouse fails")
		},
		wait: func(context.Context, int64) (*meilisearch.Task, error) {
			return nil, errors.New("wait must not be reached when ClickHouse fails")
		},
	}
	repo := &Repository{ch: isolatedCH, ms: fakeMS}

	start := time.Now()
	err = repo.deleteWorkflowLogs(ctx, uuid.NewString(), uuid.NewString())
	if status.Code(err) != codes.Internal || !strings.Contains(err.Error(), "failed to delete ClickHouse logs for workflow") {
		t.Fatalf("deleteWorkflowLogs error = %v, want Internal ClickHouse failure", err)
	}
	if len(fakeMS.enqueueCalls) != 0 || len(fakeMS.waitCalls) != 0 {
		t.Fatalf("meilisearch calls = %d enqueue / %d wait, want 0 (ClickHouse fails first)", len(fakeMS.enqueueCalls), len(fakeMS.waitCalls))
	}
	if elapsed := time.Since(start); elapsed < retryBackoff {
		t.Fatalf("deleteWorkflowLogs returned in %s, want at least one retry backoff of %s", elapsed, retryBackoff)
	}
}
