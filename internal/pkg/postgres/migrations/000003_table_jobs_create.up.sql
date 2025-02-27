DROP TYPE IF EXISTS KIND;

CREATE TYPE KIND AS ENUM ('HEARTBEAT');

CREATE TABLE IF NOT EXISTS jobs (
    id uuid PRIMARY KEY DEFAULT uuid_generate_v7(),
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE, -- Foreign key constraint
    name VARCHAR(255) NOT NULL,
    payload JSONB,
    kind KIND NOT NULL,
    interval INTEGER NOT NULL CHECK (interval >= 1), -- (in minutes)
    created_at timestamp WITHOUT TIME ZONE DEFAULT (now() AT TIME ZONE 'utc') NOT NULL,
    updated_at timestamp WITHOUT TIME ZONE DEFAULT (now() AT TIME ZONE 'utc') NOT NULL,
    terminated_at timestamp WITHOUT TIME ZONE DEFAULT NULL
);

CREATE INDEX IF NOT EXISTS idx_jobs_user_id ON jobs (user_id);
CREATE INDEX IF NOT EXISTS idx_jobs_interval ON jobs (interval);

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