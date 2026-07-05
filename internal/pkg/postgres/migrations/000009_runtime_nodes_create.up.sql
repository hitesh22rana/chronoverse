CREATE TABLE IF NOT EXISTS runtime_nodes (
    id TEXT PRIMARY KEY,
    node_name TEXT NOT NULL,
    docker_endpoint TEXT NOT NULL,
    status TEXT NOT NULL,
    last_heartbeat_at TIMESTAMP WITHOUT TIME ZONE NOT NULL,
    max_concurrency INTEGER NOT NULL CHECK (max_concurrency > 0),
    running_jobs INTEGER NOT NULL DEFAULT 0 CHECK (running_jobs >= 0),
    metadata JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMP WITHOUT TIME ZONE DEFAULT (now() AT TIME ZONE 'utc') NOT NULL,
    updated_at TIMESTAMP WITHOUT TIME ZONE DEFAULT (now() AT TIME ZONE 'utc') NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_runtime_nodes_node_name
ON runtime_nodes (node_name);

CREATE INDEX IF NOT EXISTS idx_runtime_nodes_ready_fresh
ON runtime_nodes (last_heartbeat_at, running_jobs, id)
WHERE status = 'READY';

ALTER TABLE jobs
    ADD COLUMN IF NOT EXISTS runtime_node_id TEXT DEFAULT NULL REFERENCES runtime_nodes(id),
    ADD COLUMN IF NOT EXISTS runtime_endpoint TEXT DEFAULT NULL;

ALTER TABLE workflows
    ADD COLUMN IF NOT EXISTS resolved_image_ref TEXT DEFAULT NULL,
    ADD COLUMN IF NOT EXISTS resolved_image_digest TEXT DEFAULT NULL;

CREATE INDEX IF NOT EXISTS idx_jobs_runtime_running
ON jobs (runtime_node_id, status)
WHERE status = 'RUNNING' AND runtime_node_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_jobs_expired_leases_by_runtime
ON jobs (runtime_node_id, lease_expires_at, id)
WHERE status = 'RUNNING' AND lease_expires_at IS NOT NULL;
