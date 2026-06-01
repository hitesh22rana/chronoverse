DROP TRIGGER IF EXISTS trigger_update_outbox_events ON outbox_events;
DROP FUNCTION IF EXISTS update_outbox_events_updated_at;

DROP TRIGGER IF EXISTS trigger_update_workflow_idempotency_keys ON workflow_idempotency_keys;
DROP FUNCTION IF EXISTS update_workflow_idempotency_keys_updated_at;

DROP INDEX IF EXISTS idx_outbox_events_published_cleanup;
DROP INDEX IF EXISTS idx_outbox_events_unpublished_key_order;
DROP INDEX IF EXISTS idx_processed_events_created_at;

DROP TABLE IF EXISTS workflow_failure_events;
DROP TABLE IF EXISTS processed_events;
DROP TABLE IF EXISTS outbox_events;
DROP TABLE IF EXISTS workflow_idempotency_keys;

DROP INDEX IF EXISTS idx_notifications_idempotency_key;
DROP INDEX IF EXISTS idx_jobs_expired_leases;
DROP INDEX IF EXISTS idx_jobs_active_workflow;
DROP INDEX IF EXISTS idx_jobs_runnable_pending;
DROP INDEX IF EXISTS idx_jobs_automatic_idempotency_key;
DROP INDEX IF EXISTS idx_jobs_automatic_schedule_slot;
DROP INDEX IF EXISTS idx_jobs_manual_idempotency_key;

ALTER TABLE notifications DROP COLUMN IF EXISTS idempotency_key;
ALTER TABLE jobs
    DROP COLUMN IF EXISTS last_error_message,
    DROP COLUMN IF EXISTS last_error_code,
    DROP COLUMN IF EXISTS failure_kind,
    DROP COLUMN IF EXISTS next_attempt_at,
    DROP COLUMN IF EXISTS attempts,
    DROP COLUMN IF EXISTS dispatch_attempts,
    DROP COLUMN IF EXISTS last_heartbeat_at,
    DROP COLUMN IF EXISTS lease_expires_at,
    DROP COLUMN IF EXISTS leased_by,
    DROP COLUMN IF EXISTS lease_token,
    DROP COLUMN IF EXISTS queued_at,
    DROP COLUMN IF EXISTS idempotency_key;
ALTER TABLE workflows DROP COLUMN IF EXISTS build_hash;
ALTER TABLE workflows DROP COLUMN IF EXISTS generation;

DROP TYPE IF EXISTS OUTBOX_STATUS;
DROP TYPE IF EXISTS IDEMPOTENCY_STATUS;
