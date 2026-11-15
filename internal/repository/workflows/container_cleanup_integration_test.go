//nolint:testpackage // Uses the repository's real PostgreSQL fixtures.
package workflows

import (
	"encoding/json"
	"testing"

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
