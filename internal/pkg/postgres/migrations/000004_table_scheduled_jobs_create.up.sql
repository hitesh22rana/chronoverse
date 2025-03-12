DROP TYPE IF EXISTS SCHEDULED_JOB_STATUS;

CREATE TYPE SCHEDULED_JOB_STATUS AS ENUM ('PENDING', 'QUEUED', 'RUNNING', 'COMPLETED', 'FAILED', 'CANCELLED');

CREATE TABLE IF NOT EXISTS scheduled_jobs (
    id uuid PRIMARY KEY DEFAULT uuid_generate_v7(),
    job_id uuid NOT NULL REFERENCES jobs(id) ON DELETE CASCADE, -- Foreign key constraint
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE, -- Foreign key constraint
    status SCHEDULED_JOB_STATUS DEFAULT 'PENDING' NOT NULL,
    scheduled_at timestamp WITHOUT TIME ZONE NOT NULL,
    started_at timestamp WITHOUT TIME ZONE DEFAULT NULL,
    completed_at timestamp WITHOUT TIME ZONE DEFAULT NULL,
    created_at timestamp WITHOUT TIME ZONE DEFAULT (now() AT TIME ZONE 'utc') NOT NULL,
    updated_at timestamp WITHOUT TIME ZONE DEFAULT (now() AT TIME ZONE 'utc') NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_scheduled_jobs_job_id ON scheduled_jobs (job_id);
CREATE INDEX IF NOT EXISTS idx_scheduled_jobs_user_id ON scheduled_jobs (user_id);
CREATE INDEX IF NOT EXISTS idx_scheduled_jobs_scheduled_at_status_pending ON scheduled_jobs (scheduled_at) WHERE status = 'PENDING';
CREATE INDEX IF NOT EXISTS idx_scheduled_jobs_scheduled_at_status_failed ON scheduled_jobs (scheduled_at) WHERE status = 'FAILED';
CREATE INDEX IF NOT EXISTS idx_scheduled_jobs_created_at_desc_id_desc ON scheduled_jobs (created_at DESC, id DESC);

-- Auto-update updated_at on row updates
CREATE OR REPLACE FUNCTION update_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = now() AT TIME ZONE 'utc';
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trigger_update_scheduled_jobs
BEFORE UPDATE ON scheduled_jobs
FOR EACH ROW
EXECUTE FUNCTION update_updated_at();