DROP TRIGGER IF EXISTS trigger_update_notifications ON notifications;

DROP FUNCTION IF EXISTS update_notifications_updated_at;

DROP INDEX IF EXISTS idx_notifications_user_read_created_at_desc;
DROP INDEX IF EXISTS idx_notifications_user_unread;

DROP TABLE IF EXISTS notifications;