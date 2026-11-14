CREATE TYPE IDEMPOTENCY_STATUS AS ENUM ('PROCESSING', 'COMPLETED', 'FAILED');

CREATE TABLE command_idempotency_keys (
    scope TEXT NOT NULL,
    operation TEXT NOT NULL,
    idempotency_key TEXT NOT NULL,
    request_hash TEXT NOT NULL,
    request_hash_aliases TEXT[] NOT NULL DEFAULT '{}',
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
        array_position(request_hash_aliases, NULL) IS NULL
        AND (
            cardinality(request_hash_aliases) = 0
            OR array_to_string(request_hash_aliases, ',') ~ '^[0-9a-f]{64}(,[0-9a-f]{64})*$'
        )
    ),
    CHECK (
        (status = 'PROCESSING' AND completed_at IS NULL AND expires_at IS NULL)
        OR (status <> 'PROCESSING' AND completed_at IS NOT NULL AND expires_at IS NOT NULL)
    )
);

CREATE TABLE command_idempotency_legacy_identities (
    scope TEXT NOT NULL,
    operation TEXT NOT NULL,
    idempotency_key TEXT NOT NULL,
    legacy_operation TEXT NOT NULL,
    legacy_request_hash TEXT NOT NULL,
    PRIMARY KEY (scope, operation, idempotency_key, legacy_operation),
    FOREIGN KEY (scope, operation, idempotency_key)
        REFERENCES command_idempotency_keys (scope, operation, idempotency_key)
        ON DELETE CASCADE,
    CHECK (legacy_request_hash ~ '^[0-9a-f]{64}$'),
    CHECK (
        scope LIKE 'user:%'
        AND pg_input_is_valid(substring(scope FROM length('user:') + 1), 'uuid')
    ),
    CHECK (
        CASE
            WHEN operation = 'workflow.create' THEN legacy_operation = 'create_workflow'
            WHEN operation LIKE 'workflow.update:%'
             AND legacy_operation LIKE 'update_workflow:%'
             AND pg_input_is_valid(
                 substring(operation FROM length('workflow.update:') + 1),
                 'uuid'
             )
             AND pg_input_is_valid(
                 substring(legacy_operation FROM length('update_workflow:') + 1),
                 'uuid'
             ) THEN substring(operation FROM length('workflow.update:') + 1)::uuid
                  = substring(legacy_operation FROM length('update_workflow:') + 1)::uuid
            ELSE false
        END
    )
);

CREATE INDEX idx_command_idempotency_expires_at
ON command_idempotency_keys (expires_at)
WHERE expires_at IS NOT NULL;
