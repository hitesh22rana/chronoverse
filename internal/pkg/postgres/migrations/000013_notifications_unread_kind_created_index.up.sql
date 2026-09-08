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
