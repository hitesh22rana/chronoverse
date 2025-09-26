DROP TRIGGER IF EXISTS trigger_update_jobs ON jobs;

DROP FUNCTION IF EXISTS update_jobs_updated_at;

DROP INDEX IF EXISTS idx_jobs_created_at_desc_id_desc;
DROP INDEX IF EXISTS idx_jobs_scheduled_at_status_failed;
DROP INDEX IF EXISTS idx_jobs_scheduled_at_status_pending;
DROP INDEX IF EXISTS idx_jobs_status;
DROP INDEX IF EXISTS idx_jobs_user_id;
DROP INDEX IF EXISTS idx_jobs_workflow_id;

DROP TABLE IF EXISTS jobs;

DROP TYPE IF EXISTS JOB_STATUS;