// Package commandidempotency provides the shared durable command ledger.
package commandidempotency

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

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

var requestHashPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

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

	// LegacyOperationWorkflowCreate is the workflow-create operation restored
	// for binaries using the pre-shared-ledger workflow table.
	LegacyOperationWorkflowCreate = "create_workflow"

	workflowUpdateOperationPrefix       = "workflow.update:"
	manualScheduleOperationPrefix       = "job.schedule.manual:"
	legacyWorkflowUpdateOperationPrefix = "update_workflow:"
	userScopePrefix                     = "user:"
	workflowScopePrefix                 = "workflow:"
	jobScopePrefix                      = "job:"
	workerScopePrefix                   = "worker:"
)

// WorkflowUpdateOperation returns the stable workflow-update operation name.
func WorkflowUpdateOperation(workflowID string) string {
	return workflowUpdateOperationPrefix + canonicalUUIDText(workflowID)
}

// ManualScheduleOperation returns the stable manual-schedule operation name.
func ManualScheduleOperation(workflowID string) string {
	return manualScheduleOperationPrefix + canonicalUUIDText(workflowID)
}

// LegacyWorkflowUpdateOperation returns the exact pre-shared-ledger operation
// for one accepted workflow-ID spelling. It deliberately does not canonicalize
// the input because rollback must preserve the client's original path identity.
func LegacyWorkflowUpdateOperation(rawWorkflowID string) string {
	return legacyWorkflowUpdateOperationPrefix + rawWorkflowID
}

// UserScope returns a user command scope.
func UserScope(userID string) string { return userScopePrefix + canonicalUUIDText(userID) }

// WorkflowScope returns a workflow command scope.
func WorkflowScope(workflowID string) string {
	return workflowScopePrefix + canonicalUUIDText(workflowID)
}

// JobScope returns a job command scope.
func JobScope(jobID string) string { return jobScopePrefix + canonicalUUIDText(jobID) }

// WorkerScope returns a process-specific worker command scope.
func WorkerScope(processID string) string { return workerScopePrefix + canonicalUUIDText(processID) }

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

func canonicalUUIDText(raw string) string {
	id, err := uuid.Parse(raw)
	if err != nil {
		return raw
	}
	return id.String()
}

// Reservation is a fresh command reservation or a completed replay.
type Reservation struct {
	Replay     bool
	ResourceID string
	Response   json.RawMessage
}

// LegacyIdentity is one exact operation/hash pair understood by the workflow
// idempotency implementation restored by migration 11's down migration.
type LegacyIdentity struct {
	Operation   string
	RequestHash string
}

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
	if len(key) < 1 || len(key) > 255 {
		return "", status.Error(codes.InvalidArgument, "idempotency key must be between 1 and 255 UTF-8 bytes")
	}
	return key, nil
}

// Reserve creates a PROCESSING reservation or returns an unexpired completed replay.
// Compatible request hashes are accepted only when matching an existing row;
// fresh reservations always persist the primary request hash.
func Reserve(
	ctx context.Context,
	tx pgx.Tx,
	scope, operation, rawKey, requestHash string,
	compatibleRequestHashes ...string,
) (*Reservation, error) {
	key, err := NormalizeKey(rawKey)
	if err != nil {
		return nil, err
	}
	requestHashAliases := normalizeRequestHashAliases(compatibleRequestHashes)

	query := fmt.Sprintf(`
		WITH reservation AS (
			SELECT clock_timestamp() AT TIME ZONE 'utc' AS reserved_at
		)
		INSERT INTO %s AS keys (
			scope, operation, idempotency_key, request_hash, request_hash_aliases, status,
			resource_id, response, created_at, updated_at, completed_at, expires_at
		)
		SELECT $1, $2, $3, $4, $5, 'PROCESSING', NULL, NULL,
			reservation.reserved_at, reservation.reserved_at, NULL, NULL
		FROM reservation
		ON CONFLICT (scope, operation, idempotency_key) DO UPDATE
		SET request_hash = EXCLUDED.request_hash,
			request_hash_aliases = EXCLUDED.request_hash_aliases,
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

	tag, err := tx.Exec(ctx, query, scope, operation, key, requestHash, requestHashAliases)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to reserve command idempotency key: %v", err)
	}
	if tag.RowsAffected() == 1 {
		recordOutcome(ctx, operation, "fresh")
		return &Reservation{}, nil
	}

	var storedHash, storedStatus string
	var storedHashAliases []string
	var resourceID sql.NullString
	var response []byte
	query = fmt.Sprintf(`
		SELECT request_hash, request_hash_aliases, status, resource_id, response
		FROM %s
		WHERE scope = $1 AND operation = $2 AND idempotency_key = $3
		LIMIT 1;
	`, postgres.TableCommandIdempotencyKeys)
	if err = tx.QueryRow(ctx, query, scope, operation, key).Scan(
		&storedHash,
		&storedHashAliases,
		&storedStatus,
		&resourceID,
		&response,
	); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to read command idempotency key: %v", err)
	}
	if !requestHashMatches(storedHash, storedHashAliases, requestHash, compatibleRequestHashes) {
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

func requestHashMatches(storedHash string, storedAliases []string, requestHash string, requestAliases []string) bool {
	stored := append([]string{storedHash}, storedAliases...)
	requested := append([]string{requestHash}, requestAliases...)
	for _, storedCandidate := range stored {
		for _, requestedCandidate := range requested {
			if storedCandidate == requestedCandidate {
				return true
			}
		}
	}
	return false
}

func normalizeRequestHashAliases(aliases []string) []string {
	if aliases == nil {
		return []string{}
	}
	return aliases
}

func recordOutcome(ctx context.Context, operation, outcome string) {
	trace.SpanFromContext(ctx).SetAttributes(
		attribute.String("chronoverse.idempotency.operation", operation),
		attribute.String("chronoverse.idempotency.outcome", outcome),
	)
}

// SyncLegacyIdentities records rollback-compatible identities for an accepted
// workflow command. Fresh reservations replace identities left by an expired
// key; completed replays append newly accepted UUID spellings without touching
// the parent ledger timestamps.
func SyncLegacyIdentities(
	ctx context.Context,
	tx pgx.Tx,
	scope, operation, rawKey string,
	fresh bool,
	identities ...LegacyIdentity,
) error {
	key, err := NormalizeKey(rawKey)
	if err != nil {
		return err
	}
	if len(identities) == 0 {
		return status.Error(codes.Internal, "workflow command has no rollback-compatible identity")
	}

	if fresh {
		query := fmt.Sprintf(`
			DELETE FROM %s
			WHERE scope = $1 AND operation = $2 AND idempotency_key = $3;
		`, postgres.TableCommandIdempotencyLegacyIdentities)
		if _, err = tx.Exec(ctx, query, scope, operation, key); err != nil {
			return status.Errorf(codes.Internal, "failed to reset command legacy identities: %v", err)
		}
	}

	query := fmt.Sprintf(`
		INSERT INTO %s AS identities (
			scope, operation, idempotency_key, legacy_operation, legacy_request_hash
		)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (scope, operation, idempotency_key, legacy_operation) DO UPDATE
		SET legacy_request_hash = EXCLUDED.legacy_request_hash
		WHERE identities.legacy_request_hash = EXCLUDED.legacy_request_hash;
	`, postgres.TableCommandIdempotencyLegacyIdentities)
	seen := make(map[string]string, len(identities))
	for _, identity := range identities {
		if identity.Operation == "" || !requestHashPattern.MatchString(identity.RequestHash) {
			return status.Error(codes.Internal, "invalid rollback-compatible command identity")
		}
		if existingHash, exists := seen[identity.Operation]; exists {
			if existingHash != identity.RequestHash {
				return status.Error(codes.Internal, "legacy operation resolved to multiple request hashes")
			}
			continue
		}
		seen[identity.Operation] = identity.RequestHash

		tag, execErr := tx.Exec(ctx, query, scope, operation, key, identity.Operation, identity.RequestHash)
		if execErr != nil {
			return status.Errorf(codes.Internal, "failed to store command legacy identity: %v", execErr)
		}
		if tag.RowsAffected() != 1 {
			return status.Error(codes.Internal, "legacy operation was already associated with a different request hash")
		}
	}
	return nil
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
