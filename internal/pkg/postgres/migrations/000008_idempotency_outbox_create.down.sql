DROP TRIGGER IF EXISTS trigger_update_log_analytics_offsets ON log_analytics_offsets;
DROP FUNCTION IF EXISTS update_log_analytics_offsets_updated_at;

DROP TRIGGER IF EXISTS trigger_update_outbox_events ON outbox_events;
DROP FUNCTION IF EXISTS update_outbox_events_updated_at;

DROP TRIGGER IF EXISTS trigger_update_workflow_idempotency_keys ON workflow_idempotency_keys;
DROP FUNCTION IF EXISTS update_workflow_idempotency_keys_updated_at;

DROP INDEX IF EXISTS idx_outbox_events_published_cleanup;

DROP TABLE IF EXISTS workflow_failure_events;
DROP TABLE IF EXISTS log_analytics_offsets;
DROP TABLE IF EXISTS processed_events;
DROP TABLE IF EXISTS outbox_events;
DROP TABLE IF EXISTS workflow_idempotency_keys;

DROP INDEX IF EXISTS idx_notifications_idempotency_key;
DROP INDEX IF EXISTS idx_jobs_automatic_schedule_slot;
DROP INDEX IF EXISTS idx_jobs_manual_idempotency_key;

ALTER TABLE notifications DROP COLUMN IF EXISTS idempotency_key;
ALTER TABLE jobs DROP COLUMN IF EXISTS idempotency_key;
ALTER TABLE workflows DROP COLUMN IF EXISTS build_hash;
ALTER TABLE workflows DROP COLUMN IF EXISTS generation;

DROP TYPE IF EXISTS OUTBOX_STATUS;
DROP TYPE IF EXISTS IDEMPOTENCY_STATUS;
