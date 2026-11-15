package jobs

import "context"

// GetContainerCleanupReceipt reads completion without requiring a surviving job row.
func (r *Repository) GetContainerCleanupReceipt(ctx context.Context, jobID, containerID string) (bool, error) {
	var completed bool
	err := r.pg.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM container_cleanup_receipts WHERE job_id=$1 AND container_id=$2)`, jobID, containerID).Scan(&completed)
	return completed, r.mapJobLeaseReadError(err, "get container cleanup receipt")
}

// RecordContainerCleanupReceipt is called only after Docker confirms removal or absence.
func (r *Repository) RecordContainerCleanupReceipt(ctx context.Context, jobID, containerID string) error {
	_, err := r.pg.Exec(ctx, `WITH receipt AS (
 INSERT INTO container_cleanup_receipts(job_id,container_id) VALUES($1,$2) ON CONFLICT DO NOTHING
) UPDATE outbox_events SET cleanup_state='COMPLETED',cleanup_token=NULL,cleanup_lease_until=NULL,cleanup_next_attempt_at=NULL
 WHERE topic='workflows' AND event_key='job:'||$1::text||':container:'||$2::text||':cleanup' AND cleanup_state IS NOT NULL`, jobID, containerID)
	return r.mapJobLeaseWriteError(err, "record container cleanup receipt")
}
