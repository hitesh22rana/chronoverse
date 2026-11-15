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
	_, err := r.pg.Exec(ctx, `INSERT INTO container_cleanup_receipts(job_id,container_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, jobID, containerID)
	return r.mapJobLeaseWriteError(err, "record container cleanup receipt")
}
