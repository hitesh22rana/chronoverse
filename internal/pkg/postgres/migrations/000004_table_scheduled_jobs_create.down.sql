DROP TRIGGER IF EXISTS trigger_update_scheduled_jobs ON scheduled_jobs;

DROP FUNCTION IF EXISTS update_updated_at;

DROP INDEX IF EXISTS idx_scheduled_jobs_created_at_desc_id_desc;
DROP INDEX IF EXISTS idx_scheduled_jobs_scheduled_at_status_failed;
DROP INDEX IF EXISTS idx_scheduled_jobs_scheduled_at_status_pending;
DROP INDEX IF EXISTS idx_scheduled_jobs_status;
DROP INDEX IF EXISTS idx_scheduled_jobs_user_id;
DROP INDEX IF EXISTS idx_scheduled_jobs_job_id;

DROP TABLE IF EXISTS scheduled_jobs;

DROP TYPE IF EXISTS SCHEDULED_JOB_STATUS;