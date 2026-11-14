DROP TYPE IF EXISTS WORKFLOW_BUILD_STATUS;

CREATE TYPE WORKFLOW_BUILD_STATUS AS ENUM ('QUEUED', 'STARTED', 'COMPLETED', 'FAILED', 'CANCELED');

CREATE TABLE IF NOT EXISTS workflows (
    id UUID PRIMARY KEY DEFAULT uuidv7(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name VARCHAR(255) NOT NULL,
    payload JSONB,
    kind TEXT NOT NULL,
    build_status WORKFLOW_BUILD_STATUS DEFAULT 'QUEUED' NOT NULL,
    interval INTEGER NOT NULL CHECK (interval >= 1), -- (in minutes)
    consecutive_job_failures_count INTEGER DEFAULT 0 NOT NULL CHECK (consecutive_job_failures_count >= 0),
    max_consecutive_job_failures_allowed INTEGER DEFAULT 3 NOT NULL CHECK (max_consecutive_job_failures_allowed > 0),
    created_at TIMESTAMP WITHOUT TIME ZONE DEFAULT (now() AT TIME ZONE 'utc') NOT NULL,
    updated_at TIMESTAMP WITHOUT TIME ZONE DEFAULT (now() AT TIME ZONE 'utc') NOT NULL,
    log_retention BOOLEAN NOT NULL DEFAULT TRUE,
    generation BIGINT NOT NULL DEFAULT 1,
    build_hash TEXT DEFAULT NULL,
    resolved_image_ref TEXT DEFAULT NULL,
    resolved_image_digest TEXT DEFAULT NULL,
    terminated_at TIMESTAMP WITHOUT TIME ZONE DEFAULT NULL
);

CREATE INDEX IF NOT EXISTS idx_workflows_user_id ON workflows (user_id);
CREATE INDEX IF NOT EXISTS idx_workflows_created_at_desc_id_desc ON workflows (created_at DESC, id DESC);

-- Auto-update updated_at on row updates
CREATE OR REPLACE FUNCTION update_workflows_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = now() AT TIME ZONE 'utc';
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trigger_update_workflows
BEFORE UPDATE ON workflows
FOR EACH ROW
EXECUTE FUNCTION update_workflows_updated_at();

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
