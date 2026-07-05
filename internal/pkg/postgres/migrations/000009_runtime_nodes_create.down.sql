DROP INDEX IF EXISTS idx_jobs_expired_leases_by_runtime;
DROP INDEX IF EXISTS idx_jobs_runtime_running;

ALTER TABLE workflows
    DROP COLUMN IF EXISTS resolved_image_digest,
    DROP COLUMN IF EXISTS resolved_image_ref;

ALTER TABLE jobs
    DROP COLUMN IF EXISTS runtime_endpoint,
    DROP COLUMN IF EXISTS runtime_node_id;

DROP INDEX IF EXISTS idx_runtime_nodes_ready_fresh;
DROP INDEX IF EXISTS idx_runtime_nodes_node_name;
DROP TABLE IF EXISTS runtime_nodes;
DROP TYPE IF EXISTS runtime_node_status;
