DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM workflow_idempotency_keys
        WHERE octet_length(idempotency_key) NOT BETWEEN 1 AND 255
           OR request_hash !~ '^[0-9a-f]{64}$'
    ) THEN
        RAISE EXCEPTION 'legacy workflow idempotency data violates command ledger constraints';
    END IF;
END;
$$;

DROP TRIGGER IF EXISTS trigger_update_workflow_idempotency_keys ON workflow_idempotency_keys;
DROP FUNCTION IF EXISTS update_workflow_idempotency_keys_updated_at;

CREATE TABLE command_idempotency_keys (
    scope TEXT NOT NULL,
    operation TEXT NOT NULL,
    idempotency_key TEXT NOT NULL,
    request_hash TEXT NOT NULL,
    status IDEMPOTENCY_STATUS NOT NULL DEFAULT 'PROCESSING',
    resource_id TEXT DEFAULT NULL,
    response JSONB DEFAULT NULL,
    created_at TIMESTAMP WITHOUT TIME ZONE NOT NULL,
    updated_at TIMESTAMP WITHOUT TIME ZONE NOT NULL,
    completed_at TIMESTAMP WITHOUT TIME ZONE DEFAULT NULL,
    expires_at TIMESTAMP WITHOUT TIME ZONE DEFAULT NULL,
    PRIMARY KEY (scope, operation, idempotency_key),
    CHECK (octet_length(idempotency_key) BETWEEN 1 AND 255),
    CHECK (request_hash ~ '^[0-9a-f]{64}$'),
    CHECK (
        (status = 'PROCESSING' AND completed_at IS NULL AND expires_at IS NULL)
        OR status <> 'PROCESSING'
    )
);

INSERT INTO command_idempotency_keys (
    scope,
    operation,
    idempotency_key,
    request_hash,
    status,
    resource_id,
    response,
    created_at,
    updated_at,
    completed_at,
    expires_at
)
SELECT
    'user:' || user_id::text,
    CASE
        WHEN operation = 'create_workflow' THEN 'workflow.create'
        WHEN operation LIKE 'update_workflow:%' THEN 'workflow.update:' || substring(operation FROM length('update_workflow:') + 1)
        ELSE operation
    END,
    idempotency_key,
    request_hash,
    status,
    workflow_id::text,
    response,
    created_at,
    updated_at,
    CASE WHEN status = 'COMPLETED' THEN updated_at ELSE NULL END,
    CASE WHEN status = 'PROCESSING' THEN NULL ELSE expires_at END
FROM workflow_idempotency_keys;

CREATE INDEX idx_command_idempotency_expires_at
ON command_idempotency_keys (expires_at)
WHERE expires_at IS NOT NULL;

DROP TABLE workflow_idempotency_keys;

ALTER TABLE jobs
    ADD COLUMN lease_process_instance_id UUID DEFAULT NULL;

CREATE TABLE workflow_terminal_effects (
    job_id UUID PRIMARY KEY,
    workflow_id UUID NOT NULL REFERENCES workflows(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    effect TEXT NOT NULL CHECK (effect IN ('COMPLETED', 'FAILED')),
    threshold_reached BOOLEAN DEFAULT NULL,
    created_at TIMESTAMP WITHOUT TIME ZONE DEFAULT (clock_timestamp() AT TIME ZONE 'utc') NOT NULL,
    CHECK (
        (effect = 'FAILED' AND threshold_reached IS NOT NULL)
        OR (effect = 'COMPLETED' AND threshold_reached IS NULL)
    )
);

CREATE TEMP TABLE reconciled_workflow_terminations AS
SELECT id, user_id, generation
FROM workflows
WHERE terminated_at IS NULL
  AND consecutive_job_failures_count >= max_consecutive_job_failures_allowed;

UPDATE workflows AS w
SET terminated_at = clock_timestamp() AT TIME ZONE 'utc'
FROM reconciled_workflow_terminations AS reconciled
WHERE w.id = reconciled.id;

INSERT INTO outbox_events (topic, kafka_key, event_key, payload)
SELECT
    'workflows',
    id::text,
    'workflow:' || id::text || ':TERMINATE:' || generation::text,
    jsonb_build_object(
        'EventKey', 'workflow:' || id::text || ':TERMINATE:' || generation::text,
        'ID', id::text,
        'UserID', user_id::text,
        'Action', 'TERMINATE',
        'Generation', generation,
        'JobID', '',
        'FailureKind', '',
        'ErrorCode', '',
        'ErrorMessage', ''
    )
FROM reconciled_workflow_terminations
ON CONFLICT (topic, event_key) DO NOTHING;

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM reconciled_workflow_terminations AS expected
        JOIN outbox_events AS actual
          ON actual.topic = 'workflows'
         AND actual.event_key = 'workflow:' || expected.id::text || ':TERMINATE:' || expected.generation::text
        WHERE actual.kafka_key IS DISTINCT FROM expected.id::text
           OR actual.payload->>'EventKey' IS DISTINCT FROM actual.event_key
           OR actual.payload->>'ID' IS DISTINCT FROM expected.id::text
           OR actual.payload->>'UserID' IS DISTINCT FROM expected.user_id::text
           OR actual.payload->>'Action' IS DISTINCT FROM 'TERMINATE'
           OR actual.payload->>'Generation' IS DISTINCT FROM expected.generation::text
    ) OR EXISTS (
        SELECT 1
        FROM reconciled_workflow_terminations AS expected
        WHERE NOT EXISTS (
            SELECT 1
            FROM outbox_events AS actual
            WHERE actual.topic = 'workflows'
              AND actual.event_key = 'workflow:' || expected.id::text || ':TERMINATE:' || expected.generation::text
        )
    ) THEN
        RAISE EXCEPTION 'existing workflow termination outbox row violates deterministic contract';
    END IF;
END;
$$;

INSERT INTO workflow_terminal_effects (
    job_id,
    workflow_id,
    user_id,
    effect,
    threshold_reached,
    created_at
)
SELECT job_id, workflow_id, user_id, 'FAILED', FALSE, created_at
FROM workflow_failure_events;

DROP TABLE workflow_failure_events;

DROP TABLE reconciled_workflow_terminations;
