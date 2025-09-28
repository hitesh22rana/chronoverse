-- Table to store logs for job executions
CREATE TABLE IF NOT EXISTS job_logs (
    -- IDs and metadata
    user_id UUID NOT NULL COMMENT 'ID of the user who owns the workflow',
    workflow_id UUID NOT NULL COMMENT 'ID of the parent workflow definition',
    job_id UUID NOT NULL COMMENT 'ID of the specific job execution',
    
    -- Log data
    timestamp DateTime64(3, 'UTC') NOT NULL COMMENT 'When the log entry was created (UTC).',
    message String NOT NULL COMMENT 'Log message, compressed with ZSTD.' CODEC(ZSTD(3)),
    sequence_num UInt32 NOT NULL COMMENT 'Order of log entries within a job execution',
    stream LowCardinality(String) NOT NULL COMMENT 'Stream type (stdout/stderr)',

    -- Indexes
    INDEX idx_job_logs_user (user_id) TYPE minmax GRANULARITY 1,
    INDEX idx_job_logs_workflow (workflow_id) TYPE minmax GRANULARITY 1
) 
ENGINE = MergeTree()
PARTITION BY toDate(timestamp)
ORDER BY (user_id, workflow_id, job_id, timestamp, sequence_num)
SETTINGS index_granularity = 8192;
