package workflows

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	workflowsmodel "github.com/hitesh22rana/chronoverse/internal/model/workflows"
	"github.com/hitesh22rana/chronoverse/internal/pkg/idempotency"
	"github.com/hitesh22rana/chronoverse/internal/pkg/kafka"
	"github.com/hitesh22rana/chronoverse/internal/pkg/outbox"
	"github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
)

const (
	delimiter               = '$'
	idempotencyKeyTTL       = 24 * time.Hour
	operationCreateWorkflow = "create_workflow"
)

func operationUpdateWorkflow(workflowID string) string {
	return fmt.Sprintf("update_workflow:%s", workflowID)
}

func workflowRequestHash(fields map[string]any) (string, error) {
	hash, err := idempotency.HashCanonical(fields)
	if err != nil {
		return "", status.Errorf(codes.Internal, "failed to hash idempotency request: %v", err)
	}
	return hash, nil
}

func reserveWorkflowIdempotencyKey(
	ctx context.Context,
	tx pgx.Tx,
	userID,
	operation,
	key,
	requestHash string,
) (workflowID string, replay bool, err error) {
	query := fmt.Sprintf(`
		INSERT INTO %s (user_id, operation, idempotency_key, request_hash, expires_at)
		VALUES ($1, $2, $3, $4, (now() AT TIME ZONE 'utc') + $5::interval)
		ON CONFLICT DO NOTHING;
	`, postgres.TableWorkflowIdempotencyKeys)

	ct, err := tx.Exec(ctx, query, userID, operation, key, requestHash, fmt.Sprintf("%d seconds", int(idempotencyKeyTTL.Seconds())))
	if err != nil {
		return "", false, status.Errorf(codes.Internal, "failed to reserve idempotency key: %v", err)
	}

	if ct.RowsAffected() > 0 {
		return "", false, nil
	}

	var storedHash, statusValue string
	var storedWorkflowID sql.NullString
	query = fmt.Sprintf(`
		SELECT request_hash, status, workflow_id
		FROM %s
		WHERE user_id = $1 AND operation = $2 AND idempotency_key = $3
		LIMIT 1;
	`, postgres.TableWorkflowIdempotencyKeys)
	if err = tx.QueryRow(ctx, query, userID, operation, key).Scan(&storedHash, &statusValue, &storedWorkflowID); err != nil {
		return "", false, status.Errorf(codes.Internal, "failed to fetch idempotency key: %v", err)
	}

	if storedHash != requestHash {
		return "", false, status.Error(codes.AlreadyExists, "idempotency key was already used with a different request")
	}

	if statusValue != "COMPLETED" || !storedWorkflowID.Valid {
		return "", false, status.Error(codes.Aborted, "idempotency key is already processing")
	}

	return storedWorkflowID.String, true, nil
}

func completeWorkflowIdempotencyKey(
	ctx context.Context,
	tx pgx.Tx,
	userID,
	operation,
	key,
	workflowID string,
	response any,
) error {
	responseBytes, err := json.Marshal(response)
	if err != nil {
		return status.Errorf(codes.Internal, "failed to marshal idempotency response: %v", err)
	}

	query := fmt.Sprintf(`
		UPDATE %s
		SET workflow_id = $4, response = $5::jsonb, status = 'COMPLETED'
		WHERE user_id = $1 AND operation = $2 AND idempotency_key = $3;
	`, postgres.TableWorkflowIdempotencyKeys)

	if _, err = tx.Exec(ctx, query, userID, operation, key, workflowID, string(responseBytes)); err != nil {
		return status.Errorf(codes.Internal, "failed to complete idempotency key: %v", err)
	}

	return nil
}

func workflowEventPayload(workflowID, userID string, action workflowsmodel.Action, generation int64) *workflowsmodel.WorkflowEvent {
	eventKey := idempotency.WorkflowEventKey(workflowID, action.ToString(), generation)
	return &workflowsmodel.WorkflowEvent{
		EventKey:   eventKey,
		ID:         workflowID,
		UserID:     userID,
		Action:     action,
		Generation: generation,
	}
}

func insertWorkflowOutboxEvent(ctx context.Context, tx pgx.Tx, event *workflowsmodel.WorkflowEvent) error {
	return outbox.InsertTx(ctx, tx, &outbox.Event{
		Topic:         kafka.TopicWorkflows,
		KafkaKey:      event.ID,
		EventKey:      event.EventKey,
		AggregateType: "workflow",
		AggregateID:   event.ID,
		Payload:       event,
	})
}

func encodeCursor(cursor string) string {
	if cursor == "" {
		return ""
	}

	return base64.StdEncoding.EncodeToString([]byte(cursor))
}

func extractDataFromCursor(cursor string) (string, time.Time, error) {
	parts := bytes.Split([]byte(cursor), []byte{delimiter})
	if len(parts) != 2 {
		return "", time.Time{}, status.Error(codes.InvalidArgument, "invalid cursor: expected two parts")
	}

	createdAt, err := time.Parse(time.RFC3339Nano, string(parts[1]))
	if err != nil {
		return "", time.Time{}, status.Errorf(codes.InvalidArgument, "invalid timestamp: %v", err)
	}

	return string(parts[0]), createdAt, nil
}
