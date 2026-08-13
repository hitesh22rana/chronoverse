package workflows

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
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

func workflowRequestHashes(fields map[string]any) (canonicalHash, legacyHash string, err error) {
	legacyFields := make(map[string]any, len(fields))
	canonicalFields := make(map[string]any, len(fields))
	for key, value := range fields {
		legacyFields[key] = value
		canonicalFields[key] = value
	}

	legacyHash, err = idempotency.HashCanonical(legacyFields)
	if err != nil {
		return "", "", status.Errorf(codes.Internal, "failed to hash legacy idempotency request: %v", err)
	}
	if payload, ok := canonicalFields[workflowRequestPayloadField].(string); ok {
		canonicalPayload, canonicalErr := idempotency.CanonicalJSON(payload)
		if canonicalErr != nil {
			return "", "", status.Errorf(codes.InvalidArgument, "invalid workflow payload JSON: %v", canonicalErr)
		}
		canonicalFields[workflowRequestPayloadField] = string(canonicalPayload)
	}
	canonicalHash, err = idempotency.HashCanonical(canonicalFields)
	if err != nil {
		return "", "", status.Errorf(codes.Internal, "failed to hash idempotency request: %v", err)
	}
	return canonicalHash, legacyHash, nil
}

func workflowRequestHashSet(
	canonicalFields map[string]any,
	rawUUIDFields map[string]string,
) (requestHash string, compatibleHashes []string, err error) {
	requestHash, legacyHash, err := workflowRequestHashes(canonicalFields)
	if err != nil {
		return "", nil, err
	}

	rawFields := make(map[string]any, len(canonicalFields))
	for key, value := range canonicalFields {
		rawFields[key] = value
	}
	identityChanged := false
	for key, rawValue := range rawUUIDFields {
		if canonicalValue, ok := canonicalFields[key].(string); ok && rawValue != canonicalValue {
			rawFields[key] = rawValue
			identityChanged = true
		}
	}

	compatibleHashes = []string{legacyHash}
	if identityChanged {
		_, rawLegacyHash, hashErr := workflowRequestHashes(rawFields)
		if hashErr != nil {
			return "", nil, hashErr
		}
		// Keep the exact pre-canonicalization legacy hash first so migration
		// replays and the representable down-migration identity use the same
		// spelling that the old binary hashed.
		compatibleHashes = []string{rawLegacyHash, legacyHash}
	}
	return requestHash, uniqueRequestHashes(requestHash, compatibleHashes), nil
}

func uniqueRequestHashes(primary string, hashes []string) []string {
	unique := make([]string, 0, len(hashes))
	seen := map[string]struct{}{primary: {}}
	for _, hash := range hashes {
		if _, exists := seen[hash]; exists {
			continue
		}
		seen[hash] = struct{}{}
		unique = append(unique, hash)
	}
	return unique
}

func isLegacyWorkflowCreateResponse(response json.RawMessage) bool {
	if len(response) == 0 {
		return false
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(response, &fields); err != nil || len(fields) != 1 {
		return false
	}
	_, lowerID := fields["id"]
	_, upperID := fields["ID"]
	return lowerID || upperID
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
