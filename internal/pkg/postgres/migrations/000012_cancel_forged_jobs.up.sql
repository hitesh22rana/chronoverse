-- Cancels non-terminal jobs whose user_id differs from the owning workflow's
-- user_id. Such rows could be created by manual scheduling before the
-- ownership guard existed on the MANUAL insert path.
--
-- Beyond being inert, a stale PENDING forged row would permanently block all
-- future legitimate jobs for that workflow through the scheduler's blocker
-- ordering (blocker rows are matched without an ownership predicate), so
-- pre-fix data must be cleaned rather than left in place.
UPDATE jobs AS j
SET status = 'CANCELED',
    completed_at = clock_timestamp() AT TIME ZONE 'utc',
    lease_token = NULL,
    leased_by = NULL,
    lease_process_instance_id = NULL,
    lease_expires_at = NULL,
    last_heartbeat_at = NULL,
    terminal_reason_code = 'CANCELLATION_REASON_UNAVAILABLE'
FROM workflows AS w
WHERE j.workflow_id = w.id
  AND j.user_id <> w.user_id
  AND j.status IN ('PENDING', 'QUEUED', 'RUNNING');
