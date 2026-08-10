package workflows

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"time"

	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	workflowsmodel "github.com/hitesh22rana/chronoverse/internal/model/workflows"
	"github.com/hitesh22rana/chronoverse/internal/pkg/idempotency"
	"github.com/hitesh22rana/chronoverse/internal/pkg/kafka"
	"github.com/hitesh22rana/chronoverse/internal/pkg/outbox"
)

const (
	delimiter = '$'
)

type updateWorkflowActionDecision struct {
	buildRequired      bool
	rescheduleRequired bool
	nextGeneration     int64
	buildStatus        string
}

func decideWorkflowUpdateAction(
	currentBuildHash sql.NullString,
	currentGeneration int64,
	currentInterval int32,
	currentBuildStatus string,
	reactivatingTerminatedWorkflow bool,
	newBuildHash string,
	newBuildHashValid bool,
	newInterval int32,
) updateWorkflowActionDecision {
	buildHashChanged := currentBuildHash.Valid != newBuildHashValid || currentBuildHash.String != newBuildHash
	intervalChanged := currentInterval != newInterval
	workflowNeedsBuild := currentBuildStatus != workflowsmodel.WorkflowBuildStatusCompleted.ToString() &&
		currentBuildStatus != workflowsmodel.WorkflowBuildStatusStarted.ToString()
	buildRequired := buildHashChanged || workflowNeedsBuild || reactivatingTerminatedWorkflow
	rescheduleRequired := !buildRequired &&
		intervalChanged &&
		currentBuildStatus == workflowsmodel.WorkflowBuildStatusCompleted.ToString()

	decision := updateWorkflowActionDecision{
		buildRequired:      buildRequired,
		rescheduleRequired: rescheduleRequired,
		nextGeneration:     currentGeneration,
	}
	if buildRequired || rescheduleRequired {
		decision.nextGeneration++
	}
	if buildRequired {
		decision.buildStatus = workflowsmodel.WorkflowBuildStatusQueued.ToString()
	}

	return decision
}

func workflowRequestHash(fields map[string]any) (string, error) {
	if payload, ok := fields["payload"].(string); ok {
		canonicalPayload, canonicalErr := idempotency.CanonicalJSON(payload)
		if canonicalErr != nil {
			return "", status.Errorf(codes.InvalidArgument, "invalid workflow payload JSON: %v", canonicalErr)
		}
		fields["payload"] = string(canonicalPayload)
	}
	hash, err := idempotency.HashCanonical(fields)
	if err != nil {
		return "", status.Errorf(codes.Internal, "failed to hash idempotency request: %v", err)
	}
	return hash, nil
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
		Topic:    kafka.TopicWorkflows,
		KafkaKey: event.ID,
		EventKey: event.EventKey,
		Payload:  event,
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
