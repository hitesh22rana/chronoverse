DROP TRIGGER IF EXISTS trigger_update_jobs ON jobs;

DROP FUNCTION IF EXISTS update_jobs_updated_at;

DROP INDEX IF EXISTS idx_jobs_created_at_desc_id_desc;
DROP INDEX IF EXISTS idx_jobs_user_id;

DROP TABLE IF EXISTS jobs;

DROP TYPE IF EXISTS JOB_BUILD_STATUS;