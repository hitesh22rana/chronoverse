//nolint:testpackage // Integration fixtures share package-internal repository state.
package jobs

import (
	"context"
	"testing"
	"time"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	"github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
)

// containerProvenance is the durable creation identity AttachJobContainer and
// cleanup receipts now require. Source container creation is deliberately not
// exercised here: these fixtures stand in for a runtime daemon observation.
type containerProvenance struct {
	RuntimeInstance string
	IntentID        string
}

// seedContainerProvenance records the create reservation and verified
// materialization for one container under the job's live lease and attempt.
// It never attaches the container: attachment is the effect under test.
func seedContainerProvenance(
	ctx context.Context,
	t *testing.T,
	pg *postgres.Postgres,
	repo *Repository,
	jobID, leaseToken, containerID string,
) containerProvenance {
	t.Helper()

	var attempt int32
	var workflowGeneration int64
	if err := pg.QueryRow(ctx, `
		SELECT
			j.attempts,
			w.generation
		FROM
			jobs j
			JOIN workflows w ON w.id = j.workflow_id
		WHERE
			j.id = $1
	`, jobID).Scan(&attempt, &workflowGeneration); err != nil {
		t.Fatalf("read job attempt and workflow generation: %v", err)
	}
	command := &jobsmodel.ContainerIntentCommand{
		JobID:              jobID,
		LeaseToken:         leaseToken,
		Attempt:            attempt,
		WorkflowGeneration: workflowGeneration,
		RuntimeInstance:    "daemon-" + fixtureTag(),
		RequestHash:        "request-" + fixtureTag(),
	}
	ensured, err := repo.UpdateContainerIntent(ctx, jobsmodel.ContainerIntentOperationEnsure, command)
	if err != nil {
		t.Fatalf("ensure container intent: %v", err)
	}
	command.IntentID = ensured.Intent.ID
	granted, err := repo.UpdateContainerIntent(ctx, jobsmodel.ContainerIntentOperationReserveCreate, command)
	if err != nil || !granted.Granted {
		t.Fatalf("reserve container create: %+v %v", granted, err)
	}
	command.ContainerID = containerID
	if _, err = repo.UpdateContainerIntent(ctx, jobsmodel.ContainerIntentOperationObserve, command); err != nil {
		t.Fatalf("observe container materialization: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		for _, statement := range []string{
			`DELETE FROM container_cleanup_receipts WHERE intent_id=$1`,
			`DELETE FROM runtime_capacity_holds WHERE intent_id=$1`,
			`DELETE FROM container_materializations WHERE intent_id=$1`,
			`DELETE FROM container_creation_intents WHERE id=$1`,
		} {
			if _, cleanupErr := pg.Exec(cleanupCtx, statement, ensured.Intent.ID); cleanupErr != nil {
				t.Errorf("delete container provenance: %v", cleanupErr)
			}
		}
	})
	return containerProvenance{RuntimeInstance: ensured.Intent.RuntimeInstance, IntentID: ensured.Intent.ID}
}
