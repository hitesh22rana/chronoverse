DROP TRIGGER IF EXISTS trigger_update_jobs ON jobs;

DROP FUNCTION IF EXISTS update_jobs_updated_at;

DROP INDEX IF EXISTS idx_jobs_interval;
DROP INDEX IF EXISTS idx_jobs_user_id;

DROP TABLE IF EXISTS jobs;

DROP TYPE IF EXISTS KIND;