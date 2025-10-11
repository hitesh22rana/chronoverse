-- Drop the indexes
DROP INDEX IF EXISTS idx_job_logs_user ON job_logs;
DROP INDEX IF EXISTS idx_job_logs_workflow ON job_logs;
DROP INDEX IF EXISTS idx_job_logs_stream ON job_logs;

-- Drop the table
DROP TABLE IF EXISTS job_logs;
