CREATE TYPE IDEMPOTENCY_STATUS AS ENUM ('PROCESSING', 'COMPLETED', 'FAILED');

CREATE TABLE command_idempotency_keys (
    scope TEXT NOT NULL,
    operation TEXT NOT NULL,
    idempotency_key TEXT NOT NULL,
    request_hash TEXT NOT NULL,
    status IDEMPOTENCY_STATUS NOT NULL DEFAULT 'PROCESSING',
    resource_id TEXT DEFAULT NULL,
    response JSONB DEFAULT NULL,
    created_at TIMESTAMP WITHOUT TIME ZONE NOT NULL,
    updated_at TIMESTAMP WITHOUT TIME ZONE NOT NULL,
    completed_at TIMESTAMP WITHOUT TIME ZONE DEFAULT NULL,
    expires_at TIMESTAMP WITHOUT TIME ZONE DEFAULT NULL,
    PRIMARY KEY (scope, operation, idempotency_key),
    CHECK (octet_length(idempotency_key) BETWEEN 1 AND 255),
    CHECK (request_hash ~ '^[0-9a-f]{64}$'),
    CHECK (
        (status = 'PROCESSING' AND completed_at IS NULL AND expires_at IS NULL)
        OR (status <> 'PROCESSING' AND completed_at IS NOT NULL AND expires_at IS NOT NULL)
    )
);

CREATE INDEX idx_command_idempotency_expires_at
ON command_idempotency_keys (expires_at)
WHERE expires_at IS NOT NULL;
