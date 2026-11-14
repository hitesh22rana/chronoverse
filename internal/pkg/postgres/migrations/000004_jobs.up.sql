DROP TYPE IF EXISTS JOB_STATUS;

CREATE TYPE JOB_STATUS AS ENUM ('PENDING', 'QUEUED', 'RUNNING', 'COMPLETED', 'FAILED', 'CANCELED');

CREATE TYPE JOB_TRIGGER AS ENUM ('AUTOMATIC', 'MANUAL');

CREATE TABLE IF NOT EXISTS jobs (
    id UUID PRIMARY KEY DEFAULT uuidv7(),
    workflow_id UUID NOT NULL REFERENCES workflows(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    "trigger" JOB_TRIGGER NOT NULL DEFAULT 'AUTOMATIC',
    idempotency_key TEXT DEFAULT NULL,
    queued_at TIMESTAMP WITHOUT TIME ZONE DEFAULT NULL,
    lease_token TEXT DEFAULT NULL,
    leased_by TEXT DEFAULT NULL,
    lease_expires_at TIMESTAMP WITHOUT TIME ZONE DEFAULT NULL,
    last_heartbeat_at TIMESTAMP WITHOUT TIME ZONE DEFAULT NULL,
    dispatch_attempts INTEGER NOT NULL DEFAULT 0 CHECK (dispatch_attempts >= 0),
    attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    next_attempt_at TIMESTAMP WITHOUT TIME ZONE DEFAULT NULL,
    failure_kind TEXT DEFAULT NULL,
    last_error_code TEXT DEFAULT NULL,
    last_error_message TEXT DEFAULT NULL,
    lease_process_instance_id UUID DEFAULT NULL,
    workflow_generation BIGINT DEFAULT NULL CHECK (workflow_generation IS NULL OR workflow_generation >= 0),
    runtime_node_id TEXT DEFAULT NULL REFERENCES runtime_nodes(id),
    runtime_endpoint TEXT DEFAULT NULL,
    terminal_reason_code TEXT DEFAULT NULL,
    container_id TEXT NULL, -- Unique identifier for the container, if applicable
    status JOB_STATUS DEFAULT 'PENDING' NOT NULL,
    scheduled_at TIMESTAMP WITHOUT TIME ZONE NOT NULL,
    started_at TIMESTAMP WITHOUT TIME ZONE DEFAULT NULL,
    completed_at TIMESTAMP WITHOUT TIME ZONE DEFAULT NULL,
    created_at TIMESTAMP WITHOUT TIME ZONE DEFAULT (now() AT TIME ZONE 'utc') NOT NULL,
    updated_at TIMESTAMP WITHOUT TIME ZONE DEFAULT (now() AT TIME ZONE 'utc') NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_jobs_workflow_id ON jobs (workflow_id);
CREATE INDEX IF NOT EXISTS idx_jobs_user_id ON jobs (user_id);
CREATE INDEX IF NOT EXISTS idx_jobs_status ON jobs (status);
CREATE INDEX IF NOT EXISTS idx_jobs_at_status_pending ON jobs (scheduled_at) WHERE status = 'PENDING';
CREATE INDEX IF NOT EXISTS idx_jobs_at_status_failed ON jobs (scheduled_at) WHERE status = 'FAILED';
CREATE INDEX IF NOT EXISTS idx_jobs_created_at_desc_id_desc ON jobs (created_at DESC, id DESC);

-- Auto-update updated_at on row updates
CREATE OR REPLACE FUNCTION update_jobs_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = now() AT TIME ZONE 'utc';
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trigger_update_jobs
BEFORE UPDATE ON jobs
FOR EACH ROW
EXECUTE FUNCTION update_jobs_updated_at();

CREATE INDEX IF NOT EXISTS idx_jobs_trigger ON jobs ("trigger");

CREATE UNIQUE INDEX IF NOT EXISTS idx_jobs_automatic_schedule_slot
ON jobs (workflow_id, scheduled_at, trigger)
WHERE trigger = 'AUTOMATIC';

CREATE UNIQUE INDEX IF NOT EXISTS idx_jobs_automatic_idempotency_key
ON jobs (workflow_id, idempotency_key)
WHERE trigger = 'AUTOMATIC' AND idempotency_key IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_jobs_runnable_pending
ON jobs (scheduled_at, created_at, id)
WHERE status = 'PENDING';

CREATE INDEX IF NOT EXISTS idx_jobs_active_workflow
ON jobs (workflow_id, status, scheduled_at, created_at, id)
WHERE status IN ('PENDING', 'QUEUED', 'RUNNING');

CREATE INDEX IF NOT EXISTS idx_jobs_expired_leases
ON jobs (lease_expires_at, id)
WHERE status = 'RUNNING' AND lease_expires_at IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_jobs_runtime_running
ON jobs (runtime_node_id, status)
WHERE status = 'RUNNING' AND runtime_node_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_jobs_expired_leases_by_runtime
ON jobs (runtime_node_id, lease_expires_at, id)
WHERE status = 'RUNNING' AND lease_expires_at IS NOT NULL;
