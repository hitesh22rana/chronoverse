CREATE TABLE if NOT EXISTS notifications (
    id UUID PRIMARY KEY DEFAULT uuidv7(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    idempotency_key TEXT DEFAULT NULL,
    kind TEXT NOT NULL,
    payload JSONB,
    read_at TIMESTAMP WITHOUT TIME ZONE DEFAULT NULL,
    created_at TIMESTAMP WITHOUT TIME ZONE DEFAULT (now() AT TIME ZONE 'utc') NOT NULL,
    updated_at TIMESTAMP WITHOUT TIME ZONE DEFAULT (now() AT TIME ZONE 'utc') NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_notifications_user_unread ON notifications (user_id) WHERE read_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_notifications_user_read_created_at_desc ON notifications (user_id, (read_at IS NULL) DESC, created_at DESC);

-- Auto-update updated_at on row updates
CREATE OR REPLACE FUNCTION update_notifications_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = now() AT TIME ZONE 'utc';
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trigger_update_notifications
BEFORE UPDATE ON notifications
FOR EACH ROW
EXECUTE FUNCTION update_notifications_updated_at();

CREATE UNIQUE INDEX IF NOT EXISTS idx_notifications_idempotency_key
ON notifications (user_id, idempotency_key)
WHERE idempotency_key IS NOT NULL;

-- Covers the ListNotifications hot path:
-- WHERE user_id = $1 AND read_at IS NULL AND kind = $2
-- ORDER BY created_at DESC, id DESC LIMIT N.
-- Equality on (user_id, kind) with in-order (created_at, id) avoids a
-- filter + sort over all unread notifications for the default ALERTS
-- preference. ALL-preference queries (no kind predicate) keep using
-- idx_notifications_user_read_created_at_desc.
CREATE INDEX IF NOT EXISTS idx_notifications_user_unread_kind_created_desc
ON notifications (user_id, kind, created_at DESC, id DESC)
WHERE read_at IS NULL;
