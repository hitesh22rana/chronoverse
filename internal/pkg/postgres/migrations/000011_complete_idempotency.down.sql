-- A manual command key can be reused after its 24-hour ledger retention.
-- The legacy schema can represent only one such job, so retain the newest
-- identity and clear superseded keys before restoring its permanent index.
WITH ranked_manual_keys AS (
    SELECT
        id,
        row_number() OVER (
            PARTITION BY user_id, workflow_id, idempotency_key
            ORDER BY created_at DESC, id DESC
        ) AS key_rank
    FROM jobs
    WHERE trigger = 'MANUAL'
      AND idempotency_key IS NOT NULL
)
UPDATE jobs AS j
SET idempotency_key = NULL
FROM ranked_manual_keys AS ranked
WHERE j.id = ranked.id
  AND ranked.key_rank > 1;

CREATE UNIQUE INDEX IF NOT EXISTS idx_jobs_manual_idempotency_key
ON jobs (user_id, workflow_id, idempotency_key)
WHERE trigger = 'MANUAL' AND idempotency_key IS NOT NULL;

CREATE TABLE workflow_failure_events (
    job_id UUID PRIMARY KEY,
    workflow_id UUID NOT NULL REFERENCES workflows(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TIMESTAMP WITHOUT TIME ZONE DEFAULT (now() AT TIME ZONE 'utc') NOT NULL
);

INSERT INTO workflow_failure_events (job_id, workflow_id, user_id, created_at)
SELECT job_id, workflow_id, user_id, created_at
FROM workflow_terminal_effects
WHERE effect = 'FAILED';

DROP TABLE workflow_terminal_effects;

CREATE TABLE workflow_idempotency_keys (
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

INSERT INTO workflow_idempotency_keys (
    user_id,
    operation,
    idempotency_key,
    request_hash,
    workflow_id,
    response,
    status,
    created_at,
    updated_at,
    expires_at
)
SELECT
    substring(scope FROM length('user:') + 1)::uuid,
    CASE
        WHEN operation = 'workflow.create' THEN 'create_workflow'
        WHEN operation LIKE 'workflow.update:%' THEN 'update_workflow:' || substring(operation FROM length('workflow.update:') + 1)
        ELSE operation
    END,
    idempotency_key,
    -- Alias position 1 is the canonical-ID legacy hash. The restored
    -- operation also uses canonical UUID text, so these must remain paired.
    COALESCE(request_hash_aliases[1], request_hash),
    resource_id::uuid,
    response,
    status,
    created_at,
    updated_at,
    COALESCE(expires_at, updated_at + interval '24 hours')
FROM command_idempotency_keys
WHERE scope LIKE 'user:%'
  AND (
      operation = 'workflow.create'
      OR operation LIKE 'workflow.update:%'
  );

CREATE INDEX idx_workflow_idempotency_expires_at
ON workflow_idempotency_keys (expires_at);

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

DROP TABLE command_idempotency_keys;

ALTER TABLE jobs
    DROP COLUMN lease_process_instance_id,
    DROP COLUMN workflow_generation;

-- Completed terminal identities and non-workflow command ledger records cannot
-- be represented by the legacy schema. Superseded reused manual keys are also
-- cleared above. Roll back only with traffic and workers paused.
