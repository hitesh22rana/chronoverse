package jobs

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
)

func validateCleanupIdentity(identity jobsmodel.CleanupIdentity) error {
	if identity.JobID == "" || identity.ContainerID == "" || identity.RuntimeInstance == "" || identity.IntentID == "" {
		return status.Error(codes.InvalidArgument, "job, container, runtime instance and intent are required")
	}
	return nil
}

// GetContainerCleanupReceipt reads proof for one immutable daemon/container identity.
func (r *Repository) GetContainerCleanupReceipt(ctx context.Context, identity jobsmodel.CleanupIdentity) (bool, error) {
	err := validateCleanupIdentity(identity)
	if err != nil {
		return false, err
	}
	var completed bool
	err = r.pg.QueryRow(ctx, `
SELECT EXISTS(SELECT 1
FROM container_cleanup_receipts
WHERE job_id=$1
AND container_id=$2
AND runtime_instance=$3
AND intent_id=$4)
`, identity.JobID, identity.ContainerID, identity.RuntimeInstance, identity.IntentID).Scan(&completed)
	return completed, r.mapJobLeaseReadError(err, "get container cleanup receipt")
}

// RecordContainerCleanupReceipt releases only the capacity held for the verified identity.
func (r *Repository) RecordContainerCleanupReceipt(ctx context.Context, identity jobsmodel.CleanupIdentity) error {
	err := validateCleanupIdentity(identity)
	if err != nil {
		return err
	}
	tx, err := r.pg.BeginTx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // Rollback is a no-op after commit.
	// Serialize receipt proof with Start reservation before either can install or release a hold.
	var node string
	err = tx.QueryRow(ctx, `
SELECT i.runtime_node_id
FROM container_creation_intents i
JOIN container_materializations m ON m.intent_id=i.id
WHERE i.id=$1
AND i.job_id=$2
AND m.container_id=$3
AND m.runtime_instance=$4
AND i.runtime_instance=$4
FOR UPDATE OF i
`, identity.IntentID, identity.JobID, identity.ContainerID, identity.RuntimeInstance).Scan(&node)
	if err != nil {
		return r.mapJobLeaseReadError(err, "validate cleanup provenance")
	}
	var recorded bool
	err = tx.QueryRow(ctx, `
INSERT INTO container_cleanup_receipts AS receipt(job_id,container_id,runtime_instance,intent_id)
VALUES ($1,$2,$3,$4)
ON CONFLICT (job_id,container_id,runtime_instance) DO UPDATE
 SET intent_id=receipt.intent_id
 WHERE receipt.intent_id=EXCLUDED.intent_id
RETURNING true
`, identity.JobID, identity.ContainerID, identity.RuntimeInstance, identity.IntentID).Scan(&recorded)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
UPDATE outbox_events
SET cleanup_state='COMPLETED',cleanup_token=NULL,cleanup_lease_until=NULL,cleanup_next_attempt_at=NULL
WHERE topic='workflows'
AND event_key=$1
AND cleanup_state IS NOT NULL
`, cleanupEventKey(identity.JobID, identity.ContainerID, identity.RuntimeInstance))
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, "SELECT id FROM runtime_nodes WHERE id=$1 FOR UPDATE", node); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `
UPDATE runtime_capacity_holds
SET released_at=COALESCE(released_at,clock_timestamp() AT TIME ZONE 'utc')
WHERE intent_id=$1
AND container_id=$2
AND runtime_instance=$3
`, identity.IntentID, identity.ContainerID, identity.RuntimeInstance); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, "SELECT reconcile_runtime_capacity($1)", node); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Keep this format aligned with enqueue_intent_container_cleanup in 000008_outbox.up.sql.
func cleanupEventKey(jobID, containerID, instance string) string {
	return "job:" + jobID + ":container:" + containerID + ":runtime:" + instance + ":cleanup"
}
