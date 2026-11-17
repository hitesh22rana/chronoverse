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
    cleanup_state TEXT CHECK (cleanup_state IN ('READY','PROCESSING','RETRY','QUARANTINED','COMPLETED')),
    cleanup_generation BIGINT NOT NULL DEFAULT 1 CHECK (cleanup_generation > 0),
    -- Must match cleanupMaxAttempts in internal/repository/jobs/cleanup_attempts.go.
    cleanup_attempts INTEGER NOT NULL DEFAULT 0 CHECK (cleanup_attempts BETWEEN 0 AND 3),
    cleanup_token UUID,
    cleanup_lease_until TIMESTAMP WITHOUT TIME ZONE,
    cleanup_next_attempt_at TIMESTAMP WITHOUT TIME ZONE,
    cleanup_error_code TEXT,
    cleanup_failed_at TIMESTAMP WITHOUT TIME ZONE,
    -- Zero is this generation's initial-publication lane; positive rounds pace replay and saturate without ending recovery.
    cleanup_replay_round INTEGER NOT NULL DEFAULT 0 CHECK (cleanup_replay_round BETWEEN 0 AND 63),
    cleanup_replay_due_at TIMESTAMP WITHOUT TIME ZONE,
    UNIQUE (topic, event_key)
);

-- Only short workflow claim transactions hold this scheduling turn, never Kafka I/O.
CREATE TABLE outbox_relay_turns (
    topic TEXT PRIMARY KEY CHECK (topic = 'workflows'),
    next_replay BOOLEAN NOT NULL
);
INSERT INTO outbox_relay_turns(topic, next_replay) VALUES ('workflows', FALSE);

-- Maintenance preference is reserved independently of lane deletions and publication.
CREATE TABLE outbox_cleanup_turn (
    id BOOLEAN PRIMARY KEY CHECK (id),
    next_cleanup BOOLEAN NOT NULL
);
INSERT INTO outbox_cleanup_turn(id, next_cleanup) VALUES (TRUE, FALSE);

-- Audit identity is retained independently of pruned outbox/source rows.
CREATE TABLE container_cleanup_redrives (
    event_key TEXT NOT NULL,
    job_id UUID NOT NULL,
    container_id VARCHAR(256) NOT NULL,
    runtime_instance TEXT NOT NULL CHECK (btrim(runtime_instance) <> ''),
    intent_id UUID NOT NULL,
    expected_generation BIGINT NOT NULL CHECK (expected_generation > 0),
    generation BIGINT NOT NULL CHECK (generation = expected_generation + 1),
    actor TEXT NOT NULL CHECK (btrim(actor) <> '' AND octet_length(actor) <= 256),
    reason TEXT NOT NULL CHECK (btrim(reason) <> '' AND octet_length(reason) <= 2048),
    created_at TIMESTAMP WITHOUT TIME ZONE NOT NULL DEFAULT (clock_timestamp() AT TIME ZONE 'utc'),
    PRIMARY KEY(event_key,generation)
);

-- Periodic backlog collection excludes ordinary and completed outbox traffic.
CREATE INDEX idx_outbox_cleanup_outstanding ON outbox_events(cleanup_state)
INCLUDE(status,created_at,cleanup_next_attempt_at,cleanup_lease_until)
WHERE topic='workflows' AND cleanup_state IS NOT NULL AND cleanup_state<>'COMPLETED';

CREATE INDEX IF NOT EXISTS idx_outbox_events_pending
ON outbox_events (next_attempt_at, created_at)
WHERE status = 'PENDING';

CREATE INDEX IF NOT EXISTS idx_outbox_events_failed_retry
ON outbox_events (next_attempt_at, created_at)
WHERE status = 'FAILED';

CREATE INDEX IF NOT EXISTS idx_outbox_events_processing_locked
ON outbox_events (locked_at)
WHERE status = 'PROCESSING';

CREATE INDEX idx_outbox_events_published_ordinary
ON outbox_events (published_at, id)
WHERE status = 'PUBLISHED' AND payload->>'Action' IS DISTINCT FROM 'CONTAINER_CLEANUP';

CREATE INDEX idx_outbox_events_published_completed_cleanup
ON outbox_events (published_at, id)
WHERE status = 'PUBLISHED' AND cleanup_state = 'COMPLETED'
    AND payload->>'Action' = 'CONTAINER_CLEANUP';

CREATE INDEX idx_outbox_cleanup_redrive ON outbox_events (topic,cleanup_replay_due_at,id)
WHERE status='PUBLISHED' AND cleanup_state IN ('READY','RETRY','PROCESSING');

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


-- Obligations are minted only from verified immutable observations.
CREATE FUNCTION enqueue_intent_container_cleanup(intent UUID, container TEXT)
RETURNS VOID AS $$
DECLARE
    i container_creation_intents;
    cleanup_key TEXT;
BEGIN
    SELECT * INTO i FROM container_creation_intents WHERE id=intent;
    IF NOT FOUND OR NOT EXISTS(SELECT 1 FROM container_materializations m
        WHERE m.intent_id=i.id AND m.container_id=container AND m.runtime_instance=i.runtime_instance)
       OR EXISTS(SELECT 1 FROM container_cleanup_receipts r
        WHERE r.intent_id=i.id AND r.container_id=container AND r.runtime_instance=i.runtime_instance) THEN RETURN;
    END IF;
    -- Keep this format aligned with cleanupEventKey in jobs/cleanup_receipts.go.
    cleanup_key := 'job:'||i.job_id||':container:'||container||':runtime:'||i.runtime_instance||':cleanup';
    INSERT INTO outbox_events(topic,kafka_key,event_key,cleanup_state,payload)
    VALUES('workflows',i.workflow_id::text,cleanup_key,'READY',jsonb_build_object(
        'EventKey',cleanup_key,'ID',i.workflow_id,'UserID',i.user_id,
        'JobID',i.job_id,'Action','CONTAINER_CLEANUP','CleanupGeneration',1,
        'ContainerCleanup',jsonb_build_object('ContainerID',container,
            'RuntimeNodeID',i.runtime_node_id,'RuntimeEndpoint',i.runtime_endpoint,
            'RuntimeInstance',i.runtime_instance,'IntentID',i.id,
            'Attempts',i.attempt,'LogRetention',i.log_retention)))
    ON CONFLICT(topic,event_key) DO NOTHING;
END;
$$ LANGUAGE plpgsql;

CREATE FUNCTION enqueue_intent_materializations(intent UUID)
RETURNS VOID AS $$
DECLARE m container_materializations;
BEGIN
    FOR m IN SELECT * FROM container_materializations WHERE intent_id=intent ORDER BY container_id LOOP
        PERFORM enqueue_intent_container_cleanup(m.intent_id,m.container_id);
    END LOOP;
END;
$$ LANGUAGE plpgsql;

CREATE FUNCTION enqueue_container_cleanup(job jobs, retention BOOLEAN)
RETURNS VOID AS $$
DECLARE i container_creation_intents;
BEGIN
    FOR i IN SELECT c.* FROM container_creation_intents c
        JOIN container_materializations m ON m.intent_id=c.id
        WHERE c.job_id=job.id AND m.container_id=job.container_id ORDER BY c.id LOOP
        PERFORM enqueue_intent_container_cleanup(i.id,job.container_id);
    END LOOP;
END;
$$ LANGUAGE plpgsql;

CREATE FUNCTION close_job_container_intents(job UUID)
RETURNS VOID AS $$
DECLARE i container_creation_intents;
BEGIN
    FOR i IN SELECT * FROM container_creation_intents WHERE job_id=job ORDER BY id FOR UPDATE LOOP
        UPDATE container_creation_intents SET
            closed_at=COALESCE(closed_at,clock_timestamp() AT TIME ZONE 'utc'),
            next_inspection_at=CASE WHEN closed_at IS NULL THEN clock_timestamp() AT TIME ZONE 'utc' ELSE next_inspection_at END,
            inspection_urgent=CASE WHEN closed_at IS NULL THEN TRUE ELSE inspection_urgent END,
            outcome=CASE WHEN outcome='' THEN COALESCE(
                (SELECT CASE WHEN failure_kind='USER' THEN 'REJECTED'
                    WHEN failure_kind='SYSTEM' THEN 'SYSTEM' ELSE status::text END
                 FROM jobs WHERE id=job),'CLOSED') ELSE outcome END,
            error_code=CASE WHEN error_code='' THEN COALESCE(
                (SELECT last_error_code FROM jobs WHERE id=job),'') ELSE error_code END,
            error_message=CASE WHEN error_message='' THEN COALESCE(
                (SELECT last_error_message FROM jobs WHERE id=job),'') ELSE error_message END,
            outcome_stage=CASE WHEN outcome_stage='' THEN 'SETTLEMENT' ELSE outcome_stage END
        WHERE id=i.id;
        PERFORM enqueue_intent_materializations(i.id);
    END LOOP;
END;
$$ LANGUAGE plpgsql;

CREATE FUNCTION queue_job_container_cleanup()
RETURNS TRIGGER AS $$
BEGIN
    IF OLD.status='RUNNING' AND (NEW.status<>'RUNNING' OR NEW.attempts<>OLD.attempts) THEN
        PERFORM close_job_container_intents(OLD.id);
    END IF;
    -- This trigger owns settlement/retry accounting, including cleared NEW routing.
    -- Use OLD identity so closure and capacity transfer commit atomically.
    IF OLD.runtime_node_id IS NOT NULL AND NEW.status<>'RUNNING' THEN
        PERFORM reconcile_runtime_capacity(OLD.runtime_node_id);
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER trigger_queue_job_container_cleanup
AFTER UPDATE OF status,container_id ON jobs
FOR EACH ROW EXECUTE FUNCTION queue_job_container_cleanup();

CREATE FUNCTION close_deleted_job_container_intents()
RETURNS TRIGGER AS $$
BEGIN
    PERFORM close_job_container_intents(OLD.id);
    RETURN OLD;
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER trigger_close_deleted_job_container_intents BEFORE DELETE ON jobs
FOR EACH ROW EXECUTE FUNCTION close_deleted_job_container_intents();

CREATE FUNCTION queue_deleted_workflow_container_cleanup()
RETURNS TRIGGER AS $$
DECLARE i container_creation_intents;
BEGIN
    FOR i IN SELECT * FROM container_creation_intents WHERE workflow_id=OLD.id ORDER BY job_id,id FOR UPDATE LOOP
        UPDATE container_creation_intents SET
            closed_at=COALESCE(closed_at,clock_timestamp() AT TIME ZONE 'utc'),
            next_inspection_at=CASE WHEN closed_at IS NULL THEN clock_timestamp() AT TIME ZONE 'utc' ELSE next_inspection_at END,
            inspection_urgent=CASE WHEN closed_at IS NULL THEN TRUE ELSE inspection_urgent END
        WHERE id=i.id;
        PERFORM enqueue_intent_materializations(i.id);
    END LOOP;
    IF TG_OP='DELETE' THEN RETURN OLD; END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER trigger_queue_deleted_workflow_container_cleanup BEFORE DELETE ON workflows
FOR EACH ROW EXECUTE FUNCTION queue_deleted_workflow_container_cleanup();
CREATE TRIGGER trigger_close_terminated_workflow_container_intents BEFORE UPDATE OF terminated_at ON workflows
FOR EACH ROW WHEN(NEW.terminated_at IS NOT NULL)
EXECUTE FUNCTION queue_deleted_workflow_container_cleanup();

CREATE FUNCTION reconcile_deleted_job_capacity()
RETURNS TRIGGER AS $$
BEGIN
    IF OLD.runtime_node_id IS NOT NULL THEN PERFORM reconcile_runtime_capacity(OLD.runtime_node_id); END IF;
    RETURN OLD;
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER trigger_reconcile_deleted_job_capacity AFTER DELETE ON jobs
FOR EACH ROW EXECUTE FUNCTION reconcile_deleted_job_capacity();
