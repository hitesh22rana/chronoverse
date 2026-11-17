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
    container_id VARCHAR(256) NULL, -- Unique identifier for the container, if applicable
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


-- Immutable creation authority and discovery survive source deletion.
CREATE TABLE container_creation_intents (
    id UUID PRIMARY KEY DEFAULT uuidv7(),
    job_id UUID NOT NULL,
    workflow_id UUID NOT NULL,
    user_id UUID NOT NULL,
    attempt INTEGER NOT NULL CHECK (attempt > 0),
    workflow_generation BIGINT NOT NULL,
    process_instance_id UUID NOT NULL,
    original_lease_hash TEXT NOT NULL DEFAULT '',
    runtime_node_id TEXT NOT NULL,
    runtime_endpoint TEXT NOT NULL,
    runtime_instance TEXT NOT NULL CHECK (runtime_instance <> ''),
    request_hash TEXT NOT NULL,
    image_reference TEXT NOT NULL DEFAULT '',
    name TEXT NOT NULL UNIQUE,
    log_retention BOOLEAN NOT NULL,
    container_id VARCHAR(256),
    create_reserved_at TIMESTAMP WITHOUT TIME ZONE,
    start_reserved_at TIMESTAMP WITHOUT TIME ZONE,
    closed_at TIMESTAMP WITHOUT TIME ZONE,
    outcome TEXT NOT NULL DEFAULT '',
    outcome_stage TEXT NOT NULL DEFAULT '',
    inspection_error_code TEXT NOT NULL DEFAULT '',
    inspection_error_message TEXT NOT NULL DEFAULT '',
    error_code TEXT NOT NULL DEFAULT '',
    error_message TEXT NOT NULL DEFAULT '',
    next_inspection_at TIMESTAMP WITHOUT TIME ZONE NOT NULL DEFAULT (clock_timestamp() AT TIME ZONE 'utc'),
    last_inspection_at TIMESTAMP WITHOUT TIME ZONE,
    inspection_token UUID,
    inspection_until TIMESTAMP WITHOUT TIME ZONE,
    inspection_errors INTEGER NOT NULL DEFAULT 0 CHECK (inspection_errors BETWEEN 0 AND 3),
    inspection_generation BIGINT NOT NULL DEFAULT 1 CHECK (inspection_generation > 0),
    quarantined BOOLEAN NOT NULL DEFAULT FALSE,
    redriven_by TEXT,
    redrive_reason TEXT,
    redriven_at TIMESTAMP WITHOUT TIME ZONE,
    UNIQUE(job_id, attempt)
);
CREATE INDEX idx_container_intents_due ON container_creation_intents(next_inspection_at,id)
WHERE closed_at IS NOT NULL AND NOT quarantined;
CREATE INDEX idx_container_intents_workflow ON container_creation_intents(workflow_id);

CREATE TABLE container_intent_redrives (
    id UUID PRIMARY KEY DEFAULT uuidv7(),
    intent_id UUID NOT NULL REFERENCES container_creation_intents(id),
    expected_generation BIGINT NOT NULL,
    generation BIGINT NOT NULL,
    actor TEXT NOT NULL,
    reason TEXT NOT NULL,
    created_at TIMESTAMP WITHOUT TIME ZONE NOT NULL DEFAULT (clock_timestamp() AT TIME ZONE 'utc'),
    UNIQUE(intent_id,generation)
);

CREATE TABLE container_materializations (
    intent_id UUID NOT NULL REFERENCES container_creation_intents(id),
    container_id VARCHAR(256) NOT NULL,
    runtime_instance TEXT NOT NULL,
    observed_at TIMESTAMP WITHOUT TIME ZONE NOT NULL DEFAULT (clock_timestamp() AT TIME ZONE 'utc'),
    PRIMARY KEY(intent_id,container_id,runtime_instance),
    UNIQUE(runtime_instance,container_id)
);
CREATE TABLE runtime_capacity_holds (
    intent_id UUID PRIMARY KEY REFERENCES container_creation_intents(id),
    job_id UUID NOT NULL,
    attempt INTEGER NOT NULL,
    container_id VARCHAR(256) NOT NULL,
    runtime_node_id TEXT NOT NULL,
    runtime_instance TEXT NOT NULL,
    released_at TIMESTAMP WITHOUT TIME ZONE
);
CREATE INDEX idx_runtime_capacity_holds_active ON runtime_capacity_holds(runtime_node_id)
WHERE released_at IS NULL;
CREATE TABLE container_cleanup_receipts (
    job_id UUID NOT NULL,
    container_id VARCHAR(256) NOT NULL,
    runtime_instance TEXT NOT NULL CHECK (runtime_instance <> ''),
    intent_id UUID NOT NULL REFERENCES container_creation_intents(id),
    completed_at TIMESTAMP WITHOUT TIME ZONE NOT NULL DEFAULT (clock_timestamp() AT TIME ZONE 'utc'),
    PRIMARY KEY(job_id, container_id, runtime_instance)
);
CREATE INDEX idx_container_cleanup_receipts_intent ON container_cleanup_receipts(intent_id);

-- A Start reservation installs its future hold before transmission. While its
-- job still owns the active allocation, the hold is excluded from occupancy.
CREATE FUNCTION reconcile_runtime_capacity(node TEXT)
RETURNS VOID AS $$
BEGIN
    PERFORM id FROM runtime_nodes WHERE id=node FOR UPDATE;
    UPDATE runtime_nodes rn SET running_jobs=(
        SELECT count(*)::INTEGER FROM (
            SELECT j.id::text AS allocation FROM jobs j
            WHERE j.status='RUNNING' AND j.runtime_node_id=rn.id
            UNION ALL
            SELECT h.intent_id::text FROM runtime_capacity_holds h
            WHERE h.runtime_node_id=rn.id AND h.released_at IS NULL
              AND NOT EXISTS(SELECT 1 FROM jobs j WHERE j.id=h.job_id
                  AND j.attempts=h.attempt AND j.status='RUNNING'
                  AND j.runtime_node_id=h.runtime_node_id)
        ) allocations
    ) WHERE rn.id=node;
END;
$$ LANGUAGE plpgsql;

CREATE FUNCTION workflow_container_blocked(workflow UUID)
RETURNS BOOLEAN AS $$
    SELECT EXISTS (
        SELECT 1 FROM container_creation_intents i
        WHERE i.workflow_id=workflow AND i.start_reserved_at IS NOT NULL
          AND NOT EXISTS (
            SELECT 1 FROM container_cleanup_receipts r
            WHERE r.job_id=i.job_id AND r.container_id=i.container_id
              AND r.runtime_instance=i.runtime_instance AND r.intent_id=i.id
          )
    );
$$ LANGUAGE sql STABLE;
