-- Table to store logs for job executions
CREATE TABLE IF NOT EXISTS job_logs (
    job_id UUID NOT NULL COMMENT 'ID of the specific job execution',
    workflow_id UUID NOT NULL COMMENT 'ID of the parent workflow definition',
    user_id UUID NOT NULL COMMENT 'ID of the user who owns the workflow',
    
    timestamp DateTime64(3) DEFAULT now64(3) COMMENT 'When the log entry was created',
    message String NOT NULL COMMENT 'The actual log message',
    sequence_num UInt32 NOT NULL COMMENT 'Order of log entries within a job execution',
    stream String NOT NULL COMMENT 'Stream type (stdout or stderr)',

    date Date DEFAULT toDate(timestamp) COMMENT 'Date for partitioning'
) ENGINE = MergeTree()
PARTITION BY toYYYYMM(date)
ORDER BY (job_id, timestamp, sequence_num)
SETTINGS index_granularity = 8192;

-- Indexes
CREATE INDEX IF NOT EXISTS idx_job_logs_user ON job_logs (user_id) TYPE minmax;
CREATE INDEX IF NOT EXISTS idx_job_logs_workflow ON job_logs (workflow_id) TYPE minmax;

-- This will delete logs older than 7 days
ALTER TABLE job_logs MODIFY TTL date + INTERVAL 7 DAY DELETE;