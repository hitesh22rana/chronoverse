RENAME TABLE job_logs TO job_logs_v2_rollback, job_logs_legacy TO job_logs;

DROP TABLE IF EXISTS job_logs_v2_rollback;
