DROP TYPE IF EXISTS JOB_STATUS;

CREATE TYPE JOB_STATUS AS ENUM ('PENDING', 'QUEUED', 'RUNNING', 'COMPLETED', 'FAILED', 'CANCELED');

CREATE TABLE IF NOT EXISTS jobs (
    id uuid PRIMARY KEY DEFAULT uuid_generate_v7(),
    workflow_id uuid NOT NULL REFERENCES workflows(id) ON DELETE CASCADE, -- Foreign key constraint
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE, -- Foreign key constraint
    status JOB_STATUS DEFAULT 'PENDING' NOT NULL,
    scheduled_at timestamp WITHOUT TIME ZONE NOT NULL,
    started_at timestamp WITHOUT TIME ZONE DEFAULT NULL,
    completed_at timestamp WITHOUT TIME ZONE DEFAULT NULL,
    created_at timestamp WITHOUT TIME ZONE DEFAULT (now() AT TIME ZONE 'utc') NOT NULL,
    updated_at timestamp WITHOUT TIME ZONE DEFAULT (now() AT TIME ZONE 'utc') NOT NULL
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