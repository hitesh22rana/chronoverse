DROP TYPE IF EXISTS WORKFLOW_BUILD_STATUS;

CREATE TYPE WORKFLOW_BUILD_STATUS AS ENUM ('QUEUED', 'STARTED', 'COMPLETED', 'FAILED', 'CANCELED');

CREATE TABLE IF NOT EXISTS workflows (
    id UUID PRIMARY KEY DEFAULT uuidv7(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE, -- Foreign key constraint
    name VARCHAR(255) NOT NULL,
    payload JSONB,
    kind TEXT NOT NULL,
    build_status WORKFLOW_BUILD_STATUS DEFAULT 'QUEUED' NOT NULL,
    interval INTEGER NOT NULL CHECK (interval >= 1), -- (in minutes)
    consecutive_job_failures_count INTEGER DEFAULT 0 NOT NULL CHECK (consecutive_job_failures_count >= 0),
    max_consecutive_job_failures_allowed INTEGER DEFAULT 3 NOT NULL CHECK (max_consecutive_job_failures_allowed > 0),
    created_at TIMESTAMP WITHOUT TIME ZONE DEFAULT (now() AT TIME ZONE 'utc') NOT NULL,
    updated_at TIMESTAMP WITHOUT TIME ZONE DEFAULT (now() AT TIME ZONE 'utc') NOT NULL,
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