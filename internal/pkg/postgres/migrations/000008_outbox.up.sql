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
