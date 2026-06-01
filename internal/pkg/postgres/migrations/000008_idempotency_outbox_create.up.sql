DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_type WHERE typname = 'idempotency_status') THEN
        CREATE TYPE IDEMPOTENCY_STATUS AS ENUM ('PROCESSING', 'COMPLETED', 'FAILED');
    END IF;
END;
$$;

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_type WHERE typname = 'outbox_status') THEN
        CREATE TYPE OUTBOX_STATUS AS ENUM ('PENDING', 'PROCESSING', 'PUBLISHED', 'FAILED', 'DEAD');
    ELSIF NOT EXISTS (
        SELECT 1
        FROM pg_enum e
        JOIN pg_type t ON t.oid = e.enumtypid
        WHERE t.typname = 'outbox_status' AND e.enumlabel = 'DEAD'
    ) THEN
        ALTER TYPE OUTBOX_STATUS ADD VALUE 'DEAD';
    END IF;
END;
$$;

ALTER TABLE workflows
    ADD COLUMN IF NOT EXISTS generation BIGINT NOT NULL DEFAULT 1,
    ADD COLUMN IF NOT EXISTS build_hash TEXT DEFAULT NULL;

ALTER TABLE jobs
    ADD COLUMN IF NOT EXISTS idempotency_key TEXT DEFAULT NULL,
    ADD COLUMN IF NOT EXISTS queued_at TIMESTAMP WITHOUT TIME ZONE DEFAULT NULL,
    ADD COLUMN IF NOT EXISTS lease_token TEXT DEFAULT NULL,
    ADD COLUMN IF NOT EXISTS leased_by TEXT DEFAULT NULL,
    ADD COLUMN IF NOT EXISTS lease_expires_at TIMESTAMP WITHOUT TIME ZONE DEFAULT NULL,
    ADD COLUMN IF NOT EXISTS last_heartbeat_at TIMESTAMP WITHOUT TIME ZONE DEFAULT NULL,
    ADD COLUMN IF NOT EXISTS dispatch_attempts INTEGER NOT NULL DEFAULT 0 CHECK (dispatch_attempts >= 0),
    ADD COLUMN IF NOT EXISTS attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    ADD COLUMN IF NOT EXISTS next_attempt_at TIMESTAMP WITHOUT TIME ZONE DEFAULT NULL,
    ADD COLUMN IF NOT EXISTS failure_kind TEXT DEFAULT NULL,
    ADD COLUMN IF NOT EXISTS last_error_code TEXT DEFAULT NULL,
    ADD COLUMN IF NOT EXISTS last_error_message TEXT DEFAULT NULL;

ALTER TABLE notifications
    ADD COLUMN IF NOT EXISTS idempotency_key TEXT DEFAULT NULL;

CREATE TABLE IF NOT EXISTS workflow_idempotency_keys (
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    operation TEXT NOT NULL,
    idempotency_key TEXT NOT NULL,
    request_hash TEXT NOT NULL,
    workflow_id UUID DEFAULT NULL,
    response JSONB DEFAULT NULL,
    status IDEMPOTENCY_STATUS NOT NULL DEFAULT 'PROCESSING',
    created_at TIMESTAMP WITHOUT TIME ZONE DEFAULT (now() AT TIME ZONE 'utc') NOT NULL,
    updated_at TIMESTAMP WITHOUT TIME ZONE DEFAULT (now() AT TIME ZONE 'utc') NOT NULL,
    expires_at TIMESTAMP WITHOUT TIME ZONE NOT NULL,
    PRIMARY KEY (user_id, operation, idempotency_key)
);

CREATE INDEX IF NOT EXISTS idx_workflow_idempotency_expires_at
ON workflow_idempotency_keys (expires_at);

CREATE UNIQUE INDEX IF NOT EXISTS idx_jobs_manual_idempotency_key
ON jobs (user_id, workflow_id, idempotency_key)
WHERE trigger = 'MANUAL' AND idempotency_key IS NOT NULL;

WITH duplicate_automatic_jobs AS (
    SELECT
        id,
        row_number() OVER (
            PARTITION BY workflow_id, scheduled_at, trigger
            ORDER BY created_at ASC, id ASC
        ) AS rn
    FROM jobs
    WHERE trigger = 'AUTOMATIC'
)
DELETE FROM jobs
WHERE id IN (
    SELECT id
    FROM duplicate_automatic_jobs
    WHERE rn > 1
);

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

CREATE UNIQUE INDEX IF NOT EXISTS idx_notifications_idempotency_key
ON notifications (user_id, idempotency_key)
WHERE idempotency_key IS NOT NULL;

CREATE TABLE IF NOT EXISTS outbox_events (
    id UUID PRIMARY KEY DEFAULT uuidv7(),
    topic TEXT NOT NULL,
    kafka_key TEXT NOT NULL,
    event_key TEXT NOT NULL,
    payload JSONB NOT NULL,
    status OUTBOX_STATUS NOT NULL DEFAULT 'PENDING',
    attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    next_attempt_at TIMESTAMP WITHOUT TIME ZONE DEFAULT (now() AT TIME ZONE 'utc') NOT NULL,
    locked_at TIMESTAMP WITHOUT TIME ZONE DEFAULT NULL,
    locked_by TEXT DEFAULT NULL,
    created_at TIMESTAMP WITHOUT TIME ZONE DEFAULT (now() AT TIME ZONE 'utc') NOT NULL,
    updated_at TIMESTAMP WITHOUT TIME ZONE DEFAULT (now() AT TIME ZONE 'utc') NOT NULL,
    published_at TIMESTAMP WITHOUT TIME ZONE DEFAULT NULL,
    UNIQUE (topic, event_key)
);

CREATE INDEX IF NOT EXISTS idx_outbox_events_pending
ON outbox_events (next_attempt_at, created_at)
WHERE status = 'PENDING';

CREATE INDEX IF NOT EXISTS idx_outbox_events_failed_retry
ON outbox_events (next_attempt_at, created_at)
WHERE status = 'FAILED';

CREATE INDEX IF NOT EXISTS idx_outbox_events_processing_locked
ON outbox_events (locked_at)
WHERE status = 'PROCESSING';

CREATE INDEX IF NOT EXISTS idx_outbox_events_published_cleanup
ON outbox_events (published_at)
WHERE status = 'PUBLISHED';

CREATE INDEX IF NOT EXISTS idx_outbox_events_unpublished_key_order
ON outbox_events (topic, kafka_key, created_at, id)
WHERE status <> 'PUBLISHED';

CREATE TABLE IF NOT EXISTS processed_events (
    consumer TEXT NOT NULL,
    event_key TEXT NOT NULL,
    created_at TIMESTAMP WITHOUT TIME ZONE DEFAULT (now() AT TIME ZONE 'utc') NOT NULL,
    PRIMARY KEY (consumer, event_key)
);

CREATE INDEX IF NOT EXISTS idx_processed_events_created_at
ON processed_events (created_at);

CREATE TABLE IF NOT EXISTS workflow_failure_events (
    job_id UUID PRIMARY KEY,
    workflow_id UUID NOT NULL REFERENCES workflows(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TIMESTAMP WITHOUT TIME ZONE DEFAULT (now() AT TIME ZONE 'utc') NOT NULL
);

CREATE OR REPLACE FUNCTION update_workflow_idempotency_keys_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = now() AT TIME ZONE 'utc';
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trigger_update_workflow_idempotency_keys
BEFORE UPDATE ON workflow_idempotency_keys
FOR EACH ROW
EXECUTE FUNCTION update_workflow_idempotency_keys_updated_at();

CREATE OR REPLACE FUNCTION update_outbox_events_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = now() AT TIME ZONE 'utc';
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trigger_update_outbox_events
BEFORE UPDATE ON outbox_events
FOR EACH ROW
EXECUTE FUNCTION update_outbox_events_updated_at();
