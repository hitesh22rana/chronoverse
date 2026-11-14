CREATE TABLE IF NOT EXISTS processed_events (
    consumer TEXT NOT NULL,
    event_key TEXT NOT NULL,
    created_at TIMESTAMP WITHOUT TIME ZONE DEFAULT (now() AT TIME ZONE 'utc') NOT NULL,
    PRIMARY KEY (consumer, event_key)
);

CREATE INDEX IF NOT EXISTS idx_processed_events_created_at
ON processed_events (created_at);
