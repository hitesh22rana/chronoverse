-- Drop the indexes
DROP INDEX IF EXISTS idx_job_logs_user ON job_logs;
DROP INDEX IF EXISTS idx_job_logs_workflow ON job_logs;

-- Remove TTL from job_logs table
ALTER TABLE job_logs REMOVE TTL;

-- Drop the table
DROP TABLE IF EXISTS job_logs;