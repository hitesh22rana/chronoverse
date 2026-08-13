CREATE TEMP TABLE idempotency_migration_clock AS
SELECT clock_timestamp() AT TIME ZONE 'utc' AS cutover_at;

-- Run during a maintenance window with public mutations, workers, and outbox
-- publication paused. Existing committed data is migrated in place; only
-- ambiguous or invalid legacy command state aborts the preflight below.
DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM workflow_idempotency_keys
        WHERE status <> 'COMPLETED'
          AND expires_at > (SELECT cutover_at FROM idempotency_migration_clock)
    ) THEN
        RAISE EXCEPTION 'unexpired non-completed workflow commands cannot be migrated safely';
    END IF;

    IF EXISTS (
        SELECT 1
        FROM workflow_idempotency_keys
        WHERE status = 'COMPLETED'
          AND expires_at > (SELECT cutover_at FROM idempotency_migration_clock)
          AND (
              (operation <> 'create_workflow' AND operation NOT LIKE 'update_workflow:%')
              OR idempotency_key ~ '[[:cntrl:]]'
              OR octet_length(btrim(idempotency_key, ' ')) NOT BETWEEN 1 AND 255
              OR request_hash !~ '^[0-9a-f]{64}$'
              OR workflow_id IS NULL
              OR COALESCE(response->>'id', response->>'ID') IS DISTINCT FROM workflow_id::text
          )
    ) THEN
        RAISE EXCEPTION 'legacy workflow idempotency data violates command ledger constraints';
    END IF;

    IF EXISTS (
        WITH normalized_workflow_keys AS (
            SELECT
                user_id,
                CASE
                    WHEN operation = 'create_workflow' THEN 'workflow.create'
                    ELSE 'workflow.update:' || workflow_id::text
                END AS mapped_operation,
                btrim(idempotency_key, ' ') AS normalized_key
            FROM workflow_idempotency_keys
            WHERE status = 'COMPLETED'
              AND expires_at > (SELECT cutover_at FROM idempotency_migration_clock)
        )
        SELECT 1
        FROM normalized_workflow_keys
        GROUP BY user_id, mapped_operation, normalized_key
        HAVING count(*) > 1
    ) THEN
        RAISE EXCEPTION 'legacy workflow idempotency keys collide after ASCII-space normalization';
    END IF;

    IF EXISTS (
        SELECT 1
        FROM jobs
        WHERE trigger = 'MANUAL'
          AND idempotency_key IS NOT NULL
          AND created_at + interval '24 hours' > (SELECT cutover_at FROM idempotency_migration_clock)
          AND (
              idempotency_key ~ '[[:cntrl:]]'
              OR octet_length(btrim(idempotency_key, ' ')) NOT BETWEEN 1 AND 255
          )
    ) THEN
        RAISE EXCEPTION 'legacy manual job idempotency data violates command ledger constraints';
    END IF;

    IF EXISTS (
        WITH normalized_manual_keys AS (
            SELECT
                user_id,
                workflow_id,
                btrim(idempotency_key, ' ') AS normalized_key
            FROM jobs
            WHERE trigger = 'MANUAL'
              AND idempotency_key IS NOT NULL
              AND created_at + interval '24 hours' > (SELECT cutover_at FROM idempotency_migration_clock)
        )
        SELECT 1
        FROM normalized_manual_keys
        GROUP BY user_id, workflow_id, normalized_key
        HAVING count(*) > 1
    ) THEN
        RAISE EXCEPTION 'legacy manual job idempotency keys collide after ASCII-space normalization';
    END IF;

    IF EXISTS (
        SELECT 1
        FROM jobs
        WHERE trigger = 'AUTOMATIC'
          AND idempotency_key IS NOT NULL
          AND (
              idempotency_key ~ '[[:cntrl:]]'
              OR octet_length(btrim(idempotency_key, ' ')) NOT BETWEEN 1 AND 255
          )
    ) THEN
        RAISE EXCEPTION 'legacy automatic job idempotency data violates command ledger constraints';
    END IF;

    IF EXISTS (
        SELECT 1
        FROM jobs
        WHERE trigger = 'AUTOMATIC'
          AND idempotency_key IS NOT NULL
        GROUP BY workflow_id, btrim(idempotency_key, ' ')
        HAVING count(*) > 1
    ) THEN
        RAISE EXCEPTION 'legacy automatic job idempotency keys collide after ASCII-space normalization';
    END IF;

    IF EXISTS (
        SELECT 1
        FROM notifications
        WHERE idempotency_key IS NOT NULL
          AND (
              idempotency_key ~ '[[:cntrl:]]'
              OR octet_length(btrim(idempotency_key, ' ')) NOT BETWEEN 1 AND 255
          )
    ) THEN
        RAISE EXCEPTION 'legacy notification idempotency data violates command ledger constraints';
    END IF;

    IF EXISTS (
        SELECT 1
        FROM notifications
        WHERE idempotency_key IS NOT NULL
        GROUP BY user_id, btrim(idempotency_key, ' ')
        HAVING count(*) > 1
    ) THEN
        RAISE EXCEPTION 'legacy notification idempotency keys collide after ASCII-space normalization';
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
    request_hash_aliases TEXT[] NOT NULL DEFAULT '{}',
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
        array_position(request_hash_aliases, NULL) IS NULL
        AND (
            cardinality(request_hash_aliases) = 0
            OR array_to_string(request_hash_aliases, ',') ~ '^[0-9a-f]{64}(,[0-9a-f]{64})*$'
        )
    ),
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
        WHEN operation LIKE 'update_workflow:%' THEN 'workflow.update:' || workflow_id::text
        ELSE operation
    END,
    btrim(idempotency_key, ' '),
    request_hash,
    'COMPLETED',
    workflow_id::text,
    response,
    created_at,
    updated_at,
    updated_at,
    expires_at
FROM workflow_idempotency_keys
WHERE status = 'COMPLETED'
  AND expires_at > (SELECT cutover_at FROM idempotency_migration_clock);

-- A committed MANUAL job proves its legacy schedule command succeeded. Rebuild
-- the exact new request hash and response for commands still inside their
-- 24-hour replay window before removing permanent row-level uniqueness.
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
    'job.schedule.manual:' || workflow_id::text,
    btrim(idempotency_key, ' '),
    encode(
        sha256(
            convert_to(
                '{"trigger":"MANUAL","user_id":' || to_json(user_id::text)::text
                || ',"workflow_id":' || to_json(workflow_id::text)::text || '}',
                'UTF8'
            )
        ),
        'hex'
    ),
    'COMPLETED',
    id::text,
    jsonb_build_object('id', id::text),
    created_at,
    created_at,
    created_at,
    created_at + interval '24 hours'
FROM jobs
WHERE trigger = 'MANUAL'
  AND idempotency_key IS NOT NULL
  AND created_at + interval '24 hours' > (SELECT cutover_at FROM idempotency_migration_clock);

-- Manual command keys now expire in the shared ledger and may then be reused.
-- The legacy jobs-table uniqueness constraint would retain them forever.
DROP INDEX IF EXISTS idx_jobs_manual_idempotency_key;

CREATE INDEX idx_command_idempotency_expires_at
ON command_idempotency_keys (expires_at)
WHERE expires_at IS NOT NULL;

DROP TABLE workflow_idempotency_keys;

ALTER TABLE jobs
    ADD COLUMN lease_process_instance_id UUID DEFAULT NULL,
    ADD COLUMN workflow_generation BIGINT DEFAULT NULL
        CHECK (workflow_generation IS NULL OR workflow_generation >= 0);

-- Normal event identities end in :<generation>:automatic-job, which lets the
-- upgrade retain their complete logical input. Leave nonstandard legacy keys
-- unknown; the repository will reject rather than incorrectly adopt them.
-- Preserve job.updated_at: this is schema backfill, not a domain mutation.
ALTER TABLE jobs DISABLE TRIGGER trigger_update_jobs;
WITH automatic_generations AS (
    SELECT
        id,
        substring(btrim(idempotency_key, ' ') FROM ':([0-9]+):automatic-job$') AS generation_text
    FROM jobs
    WHERE trigger = 'AUTOMATIC'
      AND idempotency_key IS NOT NULL
)
UPDATE jobs AS job
SET workflow_generation = CASE
    WHEN length(generation.generation_text) BETWEEN 1 AND 18
        THEN generation.generation_text::bigint
    ELSE NULL
END
FROM automatic_generations AS generation
WHERE job.id = generation.id;
ALTER TABLE jobs ENABLE TRIGGER trigger_update_jobs;

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
SELECT
    failure.job_id,
    failure.workflow_id,
    failure.user_id,
    'FAILED',
    workflow.consecutive_job_failures_count >= workflow.max_consecutive_job_failures_allowed,
    failure.created_at
FROM workflow_failure_events AS failure
JOIN workflows AS workflow ON workflow.id = failure.workflow_id;

DROP TABLE workflow_failure_events;

DROP TABLE reconciled_workflow_terminations;

DROP TABLE idempotency_migration_clock;
