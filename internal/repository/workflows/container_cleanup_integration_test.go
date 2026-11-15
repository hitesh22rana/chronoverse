//nolint:testpackage // Uses the repository's real PostgreSQL fixtures.
package workflows

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	jobsrepo "github.com/hitesh22rana/chronoverse/internal/repository/jobs"
	"github.com/hitesh22rana/chronoverse/internal/repository/outboxrelay"

	"github.com/hitesh22rana/chronoverse/internal/pkg/testkit"
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
			if state == "owner deletion" {
				if _, err := pg.Exec(ctx, `DELETE FROM users WHERE id=$1`, fixture.UserID); err != nil {
					t.Fatal(err)
				}
				assertDurableCleanupSnapshot(t, fixture, job, node)
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
			assertDurableCleanupSnapshot(t, fixture, job, node)
		})
	}
}

func assertDurableCleanupSnapshot(t *testing.T, fixture *workflowFixture, job, node string) {
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
		ContainerCleanup  struct {
			ContainerID, RuntimeNodeID, RuntimeEndpoint string
			Attempts                                    int
			LogRetention                                bool
		}
	}
	if err := json.Unmarshal(payloads[0], &event); err != nil {
		t.Fatal(err)
	}
	c := event.ContainerCleanup
	if event.ID != fixture.WorkflowID || event.UserID != fixture.UserID || event.JobID != job ||
		c.ContainerID != "cleanup-original" || c.RuntimeNodeID != node ||
		c.RuntimeEndpoint != "tcp://runtime:2376" || c.Attempts != 2 || !c.LogRetention {
		t.Fatalf("lost cleanup identity: %s", payloads[0])
	}
}

func TestIntegrationCleanupReceiptSurvivesPruningAndDeletion(t *testing.T) {
	ctx := t.Context()
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)
	fixture := seedWorkflowFixture(ctx, t, pg, repo, 3)
	node := seedFixtureNode(ctx, t, pg, 1)
	job := seedFixtureRunningJob(ctx, t, pg, fixture, node, "tcp://runtime:2376")
	if _, err := pg.Exec(ctx, `UPDATE jobs SET container_id='receipt-container',status='COMPLETED',lease_token=NULL,lease_expires_at=NULL WHERE id=$1`, job); err != nil {
		t.Fatal(err)
	}
	receipts := jobsrepo.New(&jobsrepo.Config{FetchLimit: 20}, nil, pg, nil, nil, nil, &jobsrepo.Services{})
	completed, err := receipts.GetContainerCleanupReceipt(ctx, job, "receipt-container")
	if err != nil || completed {
		t.Fatalf("unconfirmed cleanup has receipt: %v %v", completed, err)
	}
	if remaining := prunePublishedCleanup(t, fixture.WorkflowID); remaining != 1 {
		t.Fatalf("unfinished cleanup snapshot pruned: remaining=%d", remaining)
	}
	for range 2 {
		if err := receipts.RecordContainerCleanupReceipt(ctx, job, "receipt-container"); err != nil {
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
	assertCleanupReceiptPersistence(t, receipts, job)
}

func assertCleanupReceiptPersistence(t *testing.T, receipts *jobsrepo.Repository, job string) {
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
		completed, err := receipts.GetContainerCleanupReceipt(ctx, check.job, check.container)
		if err != nil || completed != check.want {
			t.Fatalf("receipt %s/%s=%v err=%v", check.job, check.container, completed, err)
		}
	}
	var copies int
	if err := pg.QueryRow(ctx, `SELECT count(*) FROM container_cleanup_receipts WHERE job_id=$1`, job).Scan(&copies); err != nil || copies != 1 {
		t.Fatalf("idempotent receipt count=%d err=%v", copies, err)
	}
	// Cleanup can finish after the source row has already been deleted.
	if err := receipts.RecordContainerCleanupReceipt(ctx, job, "late-confirmed-container"); err != nil {
		t.Fatal(err)
	}
	if completed, err := receipts.GetContainerCleanupReceipt(ctx, job, "late-confirmed-container"); err != nil || !completed {
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
