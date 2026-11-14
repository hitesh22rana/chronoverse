package workflows

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	workflowsmodel "github.com/hitesh22rana/chronoverse/internal/model/workflows"
	"github.com/hitesh22rana/chronoverse/internal/pkg/idempotency"
	"github.com/hitesh22rana/chronoverse/internal/pkg/kafka"
	"github.com/hitesh22rana/chronoverse/internal/pkg/kind/container"
	"github.com/hitesh22rana/chronoverse/internal/pkg/kind/heartbeat"
	"github.com/hitesh22rana/chronoverse/internal/pkg/outbox"
)

const (
	delimiter                                     = '$'
	workflowRequestWorkflowIDField                = "workflow_id"
	workflowRequestUserIDField                    = "user_id"
	workflowRequestNameField                      = "name"
	workflowRequestPayloadField                   = "payload"
	workflowRequestIntervalField                  = "interval"
	workflowRequestMaxConsecutiveJobFailuresField = "max_consecutive_job_failures_allowed"
)

type updateWorkflowActionDecision struct {
	buildRequired      bool
	rescheduleRequired bool
	nextGeneration     int64
	buildStatus        string
}

// validateWorkflowPayloadKind rejects payloads that do not match the
// workflow's stored kind, so a cross-kind update cannot poison build
// state that the executor later dispatches on the stored kind.
func validateWorkflowPayloadKind(kind, payload string) error {
	switch kind {
	case workflowsmodel.KindHeartbeat.ToString():
		if _, err := heartbeat.ExtractAndValidateHeartbeatDetails(payload); err != nil {
			return err
		}
		return nil
	case workflowsmodel.KindContainer.ToString():
		if _, err := container.ExtractAndValidateContainerDetails(payload); err != nil {
			return err
		}
		return nil
	default:
		return status.Errorf(codes.InvalidArgument, "invalid kind: %s", kind)
	}
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

// workflowRequestHash hashes canonical workflow request fields without changing the input.
func workflowRequestHash(fields map[string]any) (string, error) {
	canonicalFields := make(map[string]any, len(fields))
	for key, value := range fields {
		canonicalFields[key] = value
	}
	if payload, ok := canonicalFields[workflowRequestPayloadField].(string); ok {
		canonicalPayload, err := idempotency.CanonicalJSON(payload)
		if err != nil {
			return "", status.Errorf(codes.InvalidArgument, "invalid workflow payload JSON: %v", err)
		}
		canonicalFields[workflowRequestPayloadField] = string(canonicalPayload)
	}
	hash, err := idempotency.HashCanonical(canonicalFields)
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

// escapeLikeQuery neutralizes `%`, `_`, and the escape character itself so a
// search query cannot widen an ILIKE match or force a full scan.
func escapeLikeQuery(query string) string {
	query = strings.ReplaceAll(query, `\`, `\\`)
	query = strings.ReplaceAll(query, `%`, `\%`)
	return strings.ReplaceAll(query, `_`, `\_`)
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
