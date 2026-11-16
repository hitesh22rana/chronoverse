//nolint:testpackage // Uses the repository's real PostgreSQL fixtures.
package workflows

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	"github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
	"github.com/hitesh22rana/chronoverse/internal/pkg/testkit"
	jobsrepo "github.com/hitesh22rana/chronoverse/internal/repository/jobs"
	"github.com/hitesh22rana/chronoverse/internal/repository/outboxrelay"
)

func TestIntegrationContainerCleanupSurvivesSettlementAndDeletion(t *testing.T) {
	for _, state := range []string{"COMPLETED", "FAILED", "CANCELED", "PENDING", "owner deletion"} {
		t.Run(state, func(t *testing.T) {
			ctx := t.Context()
			pg := testkit.Postgres(t)
			repo := newTestRepository(t)
			fixture := seedWorkflowFixture(ctx, t, pg, repo, 3)
			node := seedFixtureNode(ctx, t, pg, 1)
			job := seedFixtureRunningJob(ctx, t, pg, fixture, node, "tcp://runtime:2376")
			if _, err := pg.Exec(ctx, `UPDATE jobs SET container_id='cleanup-original', attempts=2 WHERE id=$1`, job); err != nil {
				t.Fatal(err)
			}
			_, identity := seedCleanupProvenance(ctx, t, pg, job, "cleanup-original")
			if state == "owner deletion" {
				if _, err := pg.Exec(ctx, `DELETE FROM users WHERE id=$1`, fixture.UserID); err != nil {
					t.Fatal(err)
				}
				assertDurableCleanupSnapshot(t, fixture, job, node, identity)
				return
			}

			if err := repo.TerminateWorkflow(ctx, fixture.WorkflowID, fixture.UserID); err != nil {
				t.Fatal(err)
			}
			if _, err := pg.Exec(ctx, `UPDATE jobs SET status=$2::JOB_STATUS,
container_id=CASE WHEN $2='PENDING' THEN NULL ELSE container_id END,
lease_token=NULL, lease_expires_at=NULL WHERE id=$1`, job, state); err != nil {
				t.Fatal(err)
			}
			if err := repo.DeleteWorkflow(ctx, fixture.WorkflowID, fixture.UserID); err != nil {
				t.Fatal(err)
			}
			assertDurableCleanupSnapshot(t, fixture, job, node, identity)
		})
	}
}

func assertDurableCleanupSnapshot(t *testing.T, fixture *workflowFixture, job, node string, identity *jobsmodel.ContainerIntent) {
	t.Helper()
	ctx := t.Context()
	pg := testkit.Postgres(t)

	var payloads [][]byte
	rows, err := pg.Query(ctx, `SELECT payload FROM outbox_events WHERE kafka_key=$1 AND payload->>'Action'='CONTAINER_CLEANUP'`, fixture.WorkflowID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var p []byte
		if err := rows.Scan(&p); err != nil {
			t.Fatal(err)
		}
		payloads = append(payloads, p)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(payloads) != 1 {
		t.Fatalf("cleanup intents after settlement and deletion=%d, want one durable snapshot", len(payloads))
	}
	var event struct {
		ID, UserID, JobID string
		CleanupGeneration int64
		ContainerCleanup  struct {
			ContainerID, RuntimeNodeID, RuntimeEndpoint, RuntimeInstance, IntentID string
			Attempts                                                               int
			LogRetention                                                           bool
		}
	}
	if err := json.Unmarshal(payloads[0], &event); err != nil {
		t.Fatal(err)
	}
	c := event.ContainerCleanup
	require.Equal(t, int64(1), event.CleanupGeneration, "cleanup delivery generation")
	require.Equal(t, fixture.WorkflowID, event.ID)
	require.Equal(t, fixture.UserID, event.UserID)
	require.Equal(t, job, event.JobID)
	require.Equal(t, "cleanup-original", c.ContainerID)
	require.Equal(t, node, c.RuntimeNodeID)
	require.Equal(t, "tcp://runtime:2376", c.RuntimeEndpoint)
	require.Equal(t, identity.RuntimeInstance, c.RuntimeInstance)
	require.Equal(t, identity.ID, c.IntentID)
	require.Equal(t, 2, c.Attempts)
	require.True(t, c.LogRetention)
}

func TestIntegrationCleanupReceiptSurvivesPruningAndDeletion(t *testing.T) {
	ctx := t.Context()
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)
	fixture := seedWorkflowFixture(ctx, t, pg, repo, 3)
	node := seedFixtureNode(ctx, t, pg, 1)
	job := seedFixtureRunningJob(ctx, t, pg, fixture, node, "tcp://runtime:2376")
	if _, err := pg.Exec(ctx, `UPDATE jobs SET container_id='receipt-container' WHERE id=$1`, job); err != nil {
		t.Fatal(err)
	}
	receipts, identity := seedCleanupProvenance(ctx, t, pg, job, "receipt-container")
	if _, err := pg.Exec(ctx, "UPDATE jobs SET status='COMPLETED',lease_token=NULL,lease_expires_at=NULL WHERE id=$1", job); err != nil {
		t.Fatal(err)
	}
	completed, err := receipts.GetContainerCleanupReceipt(ctx, jobsmodel.CleanupIdentity{
		JobID:           job,
		ContainerID:     "receipt-container",
		RuntimeInstance: identity.RuntimeInstance,
		IntentID:        identity.ID,
	})
	if err != nil || completed {
		t.Fatalf("unconfirmed cleanup has receipt: %v %v", completed, err)
	}
	if remaining := prunePublishedCleanup(t, fixture.WorkflowID); remaining != 1 {
		t.Fatalf("unfinished cleanup snapshot pruned: remaining=%d", remaining)
	}
	for range 2 {
		if err := receipts.RecordContainerCleanupReceipt(ctx, jobsmodel.CleanupIdentity{
			JobID:           job,
			ContainerID:     "receipt-container",
			RuntimeInstance: identity.RuntimeInstance,
			IntentID:        identity.ID,
		}); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second*5)
		defer cancel()
		if _, err := pg.Exec(cleanupCtx, `DELETE FROM container_cleanup_receipts WHERE job_id=$1`, job); err != nil {
			t.Error(err)
		}
	})
	if remaining := prunePublishedCleanup(t, fixture.WorkflowID); remaining != 0 {
		t.Fatalf("completed cleanup snapshot not pruned: remaining=%d", remaining)
	}
	if err := repo.TerminateWorkflow(ctx, fixture.WorkflowID, fixture.UserID); err != nil {
		t.Fatal(err)
	}
	if err := repo.DeleteWorkflow(ctx, fixture.WorkflowID, fixture.UserID); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := pg.QueryRow(ctx, `SELECT count(*) FROM outbox_events WHERE kafka_key=$1 AND payload->>'Action'='CONTAINER_CLEANUP'`, fixture.WorkflowID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("completed cleanup recreated after pruning and deletion: %d", count)
	}
	assertCleanupReceiptPersistence(t, receipts, job, identity)
}

func assertCleanupReceiptPersistence(t *testing.T, receipts *jobsrepo.Repository, job string, identity *jobsmodel.ContainerIntent) {
	t.Helper()
	ctx := t.Context()
	pg := testkit.Postgres(t)

	for _, check := range []struct {
		job, container string
		want           bool
	}{
		{job, "receipt-container", true},
		{job, "successor-container", false},
		{uuid.NewString(), "receipt-container", false},
	} {
		completed, err := receipts.GetContainerCleanupReceipt(ctx, jobsmodel.CleanupIdentity{
			JobID:           check.job,
			ContainerID:     check.container,
			RuntimeInstance: identity.RuntimeInstance,
			IntentID:        identity.ID,
		})
		if err != nil || completed != check.want {
			t.Fatalf("receipt %s/%s=%v err=%v", check.job, check.container, completed, err)
		}
	}
	var copies int
	if err := pg.QueryRow(ctx, `SELECT count(*) FROM container_cleanup_receipts WHERE job_id=$1`, job).Scan(&copies); err != nil || copies != 1 {
		t.Fatalf("idempotent receipt count=%d err=%v", copies, err)
	}
	// A verified late materialization can finish after the source row is deleted.
	if _, err := receipts.UpdateContainerIntent(ctx, jobsmodel.ContainerIntentOperationObserve, &jobsmodel.ContainerIntentCommand{
		IntentID: identity.ID, JobID: job, ContainerID: "late-confirmed-container", RuntimeInstance: identity.RuntimeInstance,
	}); err != nil {
		t.Fatal(err)
	}
	if err := receipts.RecordContainerCleanupReceipt(ctx, jobsmodel.CleanupIdentity{
		JobID:           job,
		ContainerID:     "late-confirmed-container",
		RuntimeInstance: identity.RuntimeInstance,
		IntentID:        identity.ID,
	}); err != nil {
		t.Fatal(err)
	}
	if completed, err := receipts.GetContainerCleanupReceipt(ctx, jobsmodel.CleanupIdentity{
		JobID:           job,
		ContainerID:     "late-confirmed-container",
		RuntimeInstance: identity.RuntimeInstance,
		IntentID:        identity.ID,
	}); err != nil || !completed {
		t.Fatalf("late completion after deletion=%v err=%v", completed, err)
	}
}

func prunePublishedCleanup(t *testing.T, workflowID string) int {
	t.Helper()
	ctx := t.Context()
	pg := testkit.Postgres(t)
	if _, err := pg.Exec(ctx, `UPDATE outbox_events SET status='PUBLISHED',published_at=clock_timestamp()-interval '2 hours' WHERE kafka_key=$1`, workflowID); err != nil {
		t.Fatal(err)
	}
	relay := outboxrelay.New(&outboxrelay.Config{}, pg, nil)
	if _, err := relay.CleanupPublishedEvents(ctx, time.Hour, 10000); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := pg.QueryRow(ctx, `SELECT count(*) FROM outbox_events WHERE kafka_key=$1 AND payload->>'Action'='CONTAINER_CLEANUP'`, workflowID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func seedCleanupProvenance(ctx context.Context, t *testing.T, pg *postgres.Postgres, job, id string) (*jobsrepo.Repository, *jobsmodel.ContainerIntent) {
	t.Helper()
	repo := jobsrepo.New(&jobsrepo.Config{}, nil, pg, nil, nil, nil, &jobsrepo.Services{})
	req := &jobsmodel.ContainerIntentCommand{JobID: job, RuntimeInstance: "daemon-" + job, RequestHash: "immutable-cleanup-fixture"}
	if err := pg.QueryRow(ctx, `
 SELECT j.lease_token,j.attempts,w.generation
 FROM jobs j JOIN workflows w ON w.id=j.workflow_id
 WHERE j.id=$1
`, job).Scan(&req.LeaseToken, &req.Attempt, &req.WorkflowGeneration); err != nil {
		t.Fatal(err)
	}
	ensured, err := repo.UpdateContainerIntent(ctx, jobsmodel.ContainerIntentOperationEnsure, req)
	if err != nil {
		t.Fatal(err)
	}
	req.IntentID = ensured.Intent.ID
	grant, err := repo.UpdateContainerIntent(ctx, jobsmodel.ContainerIntentOperationReserveCreate, req)
	if err != nil || !grant.Granted {
		t.Fatalf("fixture Create grant: %+v %v", grant, err)
	}
	req.ContainerID = id
	if _, err = repo.UpdateContainerIntent(ctx, jobsmodel.ContainerIntentOperationObserve, req); err != nil {
		t.Fatal(err)
	}
	if err = repo.AttachJobContainer(ctx, job, req.LeaseToken, id, ensured.Intent.RuntimeNodeID, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	grant, err = repo.UpdateContainerIntent(ctx, jobsmodel.ContainerIntentOperationReserveStart, req)
	if err != nil || !grant.Granted {
		t.Fatalf("fixture Start grant: %+v %v", grant, err)
	}
	return repo, grant.Intent
}
