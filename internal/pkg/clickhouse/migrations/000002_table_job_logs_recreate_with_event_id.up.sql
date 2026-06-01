CREATE TABLE IF NOT EXISTS job_logs_v2 (
    -- IDs and metadata
    event_id String NOT NULL COMMENT 'Deterministic log event ID used for dedupe',
    user_id UUID NOT NULL COMMENT 'ID of the user who owns the workflow',
    workflow_id UUID NOT NULL COMMENT 'ID of the parent workflow definition',
    job_id UUID NOT NULL COMMENT 'ID of the specific job execution',

    -- Log data
    timestamp DateTime64(3, 'UTC') NOT NULL COMMENT 'When the log entry was created (UTC).',
    message String NOT NULL COMMENT 'Log message, compressed with ZSTD.' CODEC(ZSTD(3)),
    sequence_num UInt32 NOT NULL COMMENT 'Order of log entries within a job execution',
    stream LowCardinality(String) NOT NULL COMMENT 'Stream type (stdout/stderr)',

    -- Indexes
    INDEX idx_job_logs_v2_user (user_id) TYPE minmax GRANULARITY 1,
    INDEX idx_job_logs_v2_workflow (workflow_id) TYPE minmax GRANULARITY 1,
    INDEX idx_job_logs_v2_stream (stream) TYPE tokenbf_v1(512, 2, 0) GRANULARITY 1
)
ENGINE = ReplacingMergeTree()
PARTITION BY toDate(timestamp)
ORDER BY (user_id, workflow_id, job_id, sequence_num, stream, event_id)
SETTINGS index_granularity = 8192;

INSERT INTO job_logs_v2 (event_id, user_id, workflow_id, job_id, timestamp, message, sequence_num, stream)
SELECT
    concat('log:', toString(job_id), ':', stream, ':', toString(sequence_num)) AS event_id,
    user_id,
    workflow_id,
    job_id,
    timestamp,
    message,
    sequence_num,
    stream
FROM job_logs;

RENAME TABLE job_logs TO job_logs_legacy, job_logs_v2 TO job_logs;
