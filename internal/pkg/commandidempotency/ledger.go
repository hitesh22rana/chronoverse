// Package commandidempotency provides the shared durable command ledger.
package commandidempotency

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/hitesh22rana/chronoverse/internal/pkg/idempotency"
	"github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
)

const (
	// ClientCommandRetention is the replay window for client and random command identities.
	ClientCommandRetention = 24 * time.Hour
	// DefaultEventCommandRetention is the default replay window for deterministic event commands.
	DefaultEventCommandRetention = 14 * 24 * time.Hour
	// MinimumEventCommandRetention matches the supported Kafka and outbox redrive window.
	MinimumEventCommandRetention = 7 * 24 * time.Hour
)

// Stable command-ledger operation names. These are protocol values and must
// not be renamed without an explicit data migration.
const (
	OperationUserRegister            = "user.register"
	OperationWorkflowCreate          = "workflow.create"
	OperationJobScheduleAutomatic    = "job.schedule.automatic"
	OperationNotificationCreate      = "notification.create"
	OperationJobCancel               = "job.cancel"
	OperationJobClaim                = "job.claim"
	OperationJobAttachContainer      = "job.attach_container"
	OperationJobComplete             = "job.complete"
	OperationJobFail                 = "job.fail"
	OperationJobCancelClaimed        = "job.cancel_claimed"
	OperationJobReleaseForRetry      = "job.release_for_retry"
	OperationJobRecoverExpiredLeases = "job.recover_expired_leases"

	workflowUpdateOperationPrefix = "workflow.update:"
	manualScheduleOperationPrefix = "job.schedule.manual:"
	userScopePrefix               = "user:"
	workflowScopePrefix           = "workflow:"
	jobScopePrefix                = "job:"
	workerScopePrefix             = "worker:"
)

// WorkflowUpdateOperation returns the stable workflow-update operation name.
func WorkflowUpdateOperation(workflowID string) string {
	return workflowUpdateOperationPrefix + idempotency.CanonicalUUIDText(workflowID)
}

// ManualScheduleOperation returns the stable manual-schedule operation name.
func ManualScheduleOperation(workflowID string) string {
	return manualScheduleOperationPrefix + idempotency.CanonicalUUIDText(workflowID)
}

// UserScope returns a user command scope.
func UserScope(userID string) string { return userScopePrefix + idempotency.CanonicalUUIDText(userID) }

// WorkflowScope returns a workflow command scope.
func WorkflowScope(workflowID string) string {
	return workflowScopePrefix + idempotency.CanonicalUUIDText(workflowID)
}

// JobScope returns a job command scope.
func JobScope(jobID string) string { return jobScopePrefix + idempotency.CanonicalUUIDText(jobID) }

// WorkerScope returns a process-specific worker command scope.
func WorkerScope(processID string) string {
	return workerScopePrefix + idempotency.CanonicalUUIDText(processID)
}

// CanonicalUUID validates a UUID identity and returns PostgreSQL's canonical
// lowercase, hyphenated spelling. Ledger callers must use the returned value
// for scopes, operations, request hashes, mutations, and replay comparisons.
func CanonicalUUID(raw, field string) (string, error) {
	id, err := uuid.Parse(raw)
	if err != nil {
		return "", status.Errorf(codes.InvalidArgument, "%s must be a valid UUID: %v", field, err)
	}
	return id.String(), nil
}

// CanonicalWorkflowIdentity validates and canonicalizes the workflow and user
// identities that scope a workflow mutation or query. Callers must use the
// returned values for database operations, request hashes, and replay checks.
func CanonicalWorkflowIdentity(workflowID, userID string) (canonicalWorkflowID, canonicalUserID string, err error) {
	canonicalWorkflowID, err = CanonicalUUID(workflowID, "workflow ID")
	if err != nil {
		return "", "", err
	}
	canonicalUserID, err = CanonicalUUID(userID, "user ID")
	if err != nil {
		return "", "", err
	}
	return canonicalWorkflowID, canonicalUserID, nil
}

// Reservation is a fresh command reservation or a completed replay.
type Reservation struct {
	Replay     bool
	ResourceID string
	Response   json.RawMessage
}

const (
	// idempotencyKeyMinLength is the minimum normalized idempotency key length in bytes.
	idempotencyKeyMinLength = 1
	// idempotencyKeyMaxLength is the maximum normalized idempotency key length in bytes.
	idempotencyKeyMaxLength = 255
)

// NormalizeKey validates and applies the published ASCII-space normalization.
func NormalizeKey(raw string) (string, error) {
	if !utf8.ValidString(raw) {
		return "", status.Error(codes.InvalidArgument, "idempotency key must be valid UTF-8")
	}
	for _, r := range raw {
		if unicode.IsControl(r) {
			return "", status.Error(codes.InvalidArgument, "idempotency key must not contain control characters")
		}
	}

	start, end := 0, len(raw)
	for start < end && raw[start] == ' ' {
		start++
	}
	for end > start && raw[end-1] == ' ' {
		end--
	}
	key := raw[start:end]
	if len(key) < idempotencyKeyMinLength || len(key) > idempotencyKeyMaxLength {
		return "", status.Error(codes.InvalidArgument, "idempotency key must be between 1 and 255 UTF-8 bytes")
	}
	return key, nil
}

// Reserve creates a PROCESSING reservation or returns an unexpired completed replay.
// A fresh reservation must be mutated and completed in this same transaction;
// callers must never commit a PROCESSING row because it deliberately has no
// replay expiry and represents transaction-local command ownership.
func Reserve(
	ctx context.Context,
	tx pgx.Tx,
	scope, operation, rawKey, requestHash string,
) (*Reservation, error) {
	key, err := NormalizeKey(rawKey)
	if err != nil {
		return nil, err
	}
	query := fmt.Sprintf(`
		WITH reservation AS (
			SELECT clock_timestamp() AT TIME ZONE 'utc' AS reserved_at
		)
		INSERT INTO %s AS keys (
			scope, operation, idempotency_key, request_hash, status,
			resource_id, response, created_at, updated_at, completed_at, expires_at
		)
		SELECT $1, $2, $3, $4, 'PROCESSING', NULL, NULL,
			reservation.reserved_at, reservation.reserved_at, NULL, NULL
		FROM reservation
		ON CONFLICT (scope, operation, idempotency_key) DO UPDATE
		SET request_hash = EXCLUDED.request_hash,
			status = 'PROCESSING',
			resource_id = NULL,
			response = NULL,
			created_at = EXCLUDED.created_at,
			updated_at = EXCLUDED.updated_at,
			completed_at = NULL,
			expires_at = NULL
		WHERE keys.expires_at IS NOT NULL
		  AND keys.expires_at <= clock_timestamp() AT TIME ZONE 'utc';
	`, postgres.TableCommandIdempotencyKeys)

	tag, err := tx.Exec(ctx, query, scope, operation, key, requestHash)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to reserve command idempotency key: %v", err)
	}
	if tag.RowsAffected() == 1 {
		recordOutcome(ctx, operation, "fresh")
		return &Reservation{}, nil
	}

	var storedHash, storedStatus string
	var resourceID sql.NullString
	var response []byte
	query = fmt.Sprintf(`
		SELECT request_hash, status, resource_id, response
		FROM %s
		WHERE scope = $1 AND operation = $2 AND idempotency_key = $3
		LIMIT 1;
	`, postgres.TableCommandIdempotencyKeys)
	if err = tx.QueryRow(ctx, query, scope, operation, key).Scan(
		&storedHash,
		&storedStatus,
		&resourceID,
		&response,
	); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to read command idempotency key: %v", err)
	}
	if storedHash != requestHash {
		recordOutcome(ctx, operation, "conflict")
		return nil, status.Error(codes.AlreadyExists, "idempotency key was already used with a different request")
	}
	if storedStatus != "COMPLETED" {
		recordOutcome(ctx, operation, "in_progress")
		return nil, status.Error(codes.Aborted, "idempotency command is still processing")
	}
	recordOutcome(ctx, operation, "replay")
	return &Reservation{Replay: true, ResourceID: resourceID.String, Response: response}, nil
}

func recordOutcome(ctx context.Context, operation, outcome string) {
	trace.SpanFromContext(ctx).SetAttributes(
		attribute.String("chronoverse.idempotency.operation", operation),
		attribute.String("chronoverse.idempotency.outcome", outcome),
	)
}

// Complete compare-and-set completes exactly one owned PROCESSING reservation.
func Complete(
	ctx context.Context,
	tx pgx.Tx,
	scope, operation, rawKey, requestHash, resourceID string,
	response any,
	retention time.Duration,
) error {
	if retention <= 0 || retention.Microseconds() <= 0 {
		return status.Error(codes.Internal, "command idempotency retention must be positive")
	}
	key, err := NormalizeKey(rawKey)
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		return status.Errorf(codes.Internal, "failed to encode idempotency response: %v", err)
	}

	query := fmt.Sprintf(`
		WITH completion AS (
			SELECT clock_timestamp() AT TIME ZONE 'utc' AS completed_at
		)
		UPDATE %s
		SET status = 'COMPLETED',
			resource_id = NULLIF($5, ''),
			response = $6::jsonb,
			completed_at = completion.completed_at,
			updated_at = completion.completed_at,
			expires_at = completion.completed_at + ($7::bigint * interval '1 microsecond')
		FROM completion
		WHERE scope = $1
		  AND operation = $2
		  AND idempotency_key = $3
		  AND request_hash = $4
		  AND status = 'PROCESSING';
	`, postgres.TableCommandIdempotencyKeys)
	tag, err := tx.Exec(ctx, query, scope, operation, key, requestHash, resourceID, encoded, retention.Microseconds())
	if err != nil {
		return status.Errorf(codes.Internal, "failed to complete command idempotency key: %v", err)
	}
	if tag.RowsAffected() != 1 {
		return status.Error(codes.Internal, "command idempotency completion invariant violated")
	}
	return nil
}
