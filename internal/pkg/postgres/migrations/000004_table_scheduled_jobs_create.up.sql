DROP TYPE IF EXISTS SCHEDULED_JOB_STATUS;

CREATE TYPE SCHEDULED_JOB_STATUS AS ENUM ('PENDING', 'QUEUED', 'RUNNING', 'SUCCESS', 'FAILED', 'RETRYING', 'CANCELLED');

CREATE TABLE IF NOT EXISTS scheduled_jobs (
    id uuid PRIMARY KEY DEFAULT uuid_generate_v4(),
    job_id uuid NOT NULL REFERENCES jobs(id) ON DELETE CASCADE, -- Foreign key constraint
    status SCHEDULED_JOB_STATUS DEFAULT 'PENDING' NOT NULL,
    scheduled_at timestamp WITHOUT TIME ZONE NOT NULL,
    retry_count INTEGER DEFAULT 0 NOT NULL CHECK (retry_count >= 0),
    max_retry INTEGER DEFAULT 0 NOT NULL CHECK (max_retry >= 0 AND max_retry >= retry_count),
    started_at timestamp WITHOUT TIME ZONE DEFAULT NULL,
    completed_at timestamp WITHOUT TIME ZONE DEFAULT NULL,
    created_at timestamp WITHOUT TIME ZONE DEFAULT (now() AT TIME ZONE 'utc') NOT NULL,
    updated_at timestamp WITHOUT TIME ZONE DEFAULT (now() AT TIME ZONE 'utc') NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_scheduled_jobs_job_id ON scheduled_jobs (job_id);
CREATE INDEX IF NOT EXISTS idx_scheduled_jobs_scheduled_at ON scheduled_jobs (scheduled_at) WHERE status = 'PENDING';

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