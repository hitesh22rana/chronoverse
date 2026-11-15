DROP TRIGGER IF EXISTS trigger_queue_deleted_workflow_container_cleanup ON workflows;
DROP TRIGGER IF EXISTS trigger_queue_job_container_cleanup ON jobs;
DROP FUNCTION IF EXISTS queue_deleted_workflow_container_cleanup;
DROP FUNCTION IF EXISTS queue_job_container_cleanup;
DROP FUNCTION IF EXISTS enqueue_container_cleanup(jobs, BOOLEAN);
DROP TABLE IF EXISTS outbox_events;
DROP FUNCTION IF EXISTS update_outbox_events_updated_at;
DROP TYPE IF EXISTS OUTBOX_STATUS;
