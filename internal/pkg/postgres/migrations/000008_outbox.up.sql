CREATE TYPE OUTBOX_STATUS AS ENUM ('PENDING', 'PROCESSING', 'PUBLISHED', 'FAILED', 'DEAD');

CREATE TABLE IF NOT EXISTS outbox_events (
    id UUID PRIMARY KEY DEFAULT uuidv7(),
    topic TEXT NOT NULL,
    kafka_key TEXT NOT NULL,
    event_key TEXT NOT NULL,
    payload JSONB NOT NULL,
    status OUTBOX_STATUS NOT NULL DEFAULT 'PENDING',
    attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    next_attempt_at TIMESTAMP WITHOUT TIME ZONE DEFAULT (now() AT TIME ZONE 'utc') NOT NULL,
    locked_at TIMESTAMP WITHOUT TIME ZONE DEFAULT NULL,
    locked_by TEXT DEFAULT NULL,
    created_at TIMESTAMP WITHOUT TIME ZONE DEFAULT (now() AT TIME ZONE 'utc') NOT NULL,
    updated_at TIMESTAMP WITHOUT TIME ZONE DEFAULT (now() AT TIME ZONE 'utc') NOT NULL,
    published_at TIMESTAMP WITHOUT TIME ZONE DEFAULT NULL,
    UNIQUE (topic, event_key)
);

CREATE INDEX IF NOT EXISTS idx_outbox_events_pending
ON outbox_events (next_attempt_at, created_at)
WHERE status = 'PENDING';

CREATE INDEX IF NOT EXISTS idx_outbox_events_failed_retry
ON outbox_events (next_attempt_at, created_at)
WHERE status = 'FAILED';

CREATE INDEX IF NOT EXISTS idx_outbox_events_processing_locked
ON outbox_events (locked_at)
WHERE status = 'PROCESSING';

CREATE INDEX IF NOT EXISTS idx_outbox_events_published_cleanup
ON outbox_events (published_at)
WHERE status = 'PUBLISHED';

CREATE INDEX IF NOT EXISTS idx_outbox_events_unpublished_key_order
ON outbox_events (topic, kafka_key, created_at, id)
WHERE status <> 'PUBLISHED';

CREATE OR REPLACE FUNCTION update_outbox_events_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = now() AT TIME ZONE 'utc';
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trigger_update_outbox_events
BEFORE UPDATE ON outbox_events
FOR EACH ROW
EXECUTE FUNCTION update_outbox_events_updated_at();

-- Cleanup snapshots share workflow ordering and survive cascading deletion.
CREATE FUNCTION enqueue_container_cleanup(job jobs, retention BOOLEAN)
RETURNS VOID AS $$
DECLARE
    cleanup_key TEXT := 'job:' || job.id || ':container:' || job.container_id || ':cleanup';
BEGIN
    IF COALESCE(job.container_id, '') = '' OR EXISTS (
        SELECT 1 FROM container_cleanup_receipts
        WHERE job_id = job.id AND container_id = job.container_id
    ) THEN
        RETURN;
    END IF;
    INSERT INTO outbox_events (topic, kafka_key, event_key, payload)
    VALUES ('workflows', job.workflow_id::TEXT, cleanup_key, jsonb_build_object(
        'EventKey', cleanup_key, 'ID', job.workflow_id, 'UserID', job.user_id,
        'JobID', job.id, 'Action', 'CONTAINER_CLEANUP',
        'ContainerCleanup', jsonb_build_object(
            'ContainerID', job.container_id, 'RuntimeNodeID', job.runtime_node_id,
            'RuntimeEndpoint', job.runtime_endpoint, 'Attempts', job.attempts,
            'LogRetention', retention)))
    ON CONFLICT (topic, event_key) DO NOTHING;
END;
$$ LANGUAGE plpgsql;

CREATE FUNCTION queue_job_container_cleanup()
RETURNS TRIGGER AS $$
DECLARE
    retention BOOLEAN;
BEGIN
    SELECT log_retention INTO retention FROM workflows WHERE id = NEW.workflow_id;
    IF OLD.container_id IS DISTINCT FROM NEW.container_id THEN
        PERFORM enqueue_container_cleanup(OLD, retention);
    END IF;
    IF NEW.status IN ('COMPLETED', 'FAILED', 'CANCELED') AND
       (OLD.status IS DISTINCT FROM NEW.status OR OLD.container_id IS DISTINCT FROM NEW.container_id) THEN
        PERFORM enqueue_container_cleanup(NEW, retention);
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trigger_queue_job_container_cleanup
AFTER UPDATE OF status, container_id ON jobs
FOR EACH ROW EXECUTE FUNCTION queue_job_container_cleanup();

CREATE FUNCTION queue_deleted_workflow_container_cleanup()
RETURNS TRIGGER AS $$
DECLARE
    job jobs;
BEGIN
    FOR job IN SELECT * FROM jobs WHERE workflow_id = OLD.id LOOP
        PERFORM enqueue_container_cleanup(job, OLD.log_retention);
    END LOOP;
    RETURN OLD;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trigger_queue_deleted_workflow_container_cleanup
BEFORE DELETE ON workflows
FOR EACH ROW EXECUTE FUNCTION queue_deleted_workflow_container_cleanup();
