package idempotency

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/hitesh22rana/chronoverse/internal/pkg/kind/container"
)

// HashCanonical returns a SHA-256 hash for a JSON-canonical value.
func HashCanonical(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}

	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// ContainerBuildHash returns the build hash for a container workflow payload.
func ContainerBuildHash(payload string) (string, error) {
	details, err := container.ExtractAndValidateContainerDetails(payload)
	if err != nil {
		return "", err
	}

	return HashCanonical(map[string]string{
		"image": details.Image,
	})
}

// WorkflowBuildHash returns the build hash for the workflow kind and payload.
func WorkflowBuildHash(kind, payload string) (string, error) {
	switch kind {
	case "CONTAINER":
		return ContainerBuildHash(payload)
	case "HEARTBEAT":
		return "", nil
	default:
		return "", status.Errorf(codes.InvalidArgument, "invalid kind: %s", kind)
	}
}

// WorkflowEventKey returns the deterministic idempotency key for a workflow event.
func WorkflowEventKey(workflowID, action string, generation int64) string {
	if generation > 0 {
		return fmt.Sprintf("workflow:%s:%s:%d", workflowID, action, generation)
	}
	return fmt.Sprintf("workflow:%s:%s", workflowID, action)
}

// JobDispatchEventKey returns the deterministic idempotency key for dispatching a job.
func JobDispatchEventKey(jobID string) string {
	return fmt.Sprintf("job:%s:dispatch", jobID)
}

// JobCompletedAnalyticsEventKey returns the deterministic analytics event key for a completed job.
func JobCompletedAnalyticsEventKey(jobID string) string {
	return fmt.Sprintf("analytics:job:%s:completed", jobID)
}

// WorkflowAnalyticsEventKey returns the deterministic analytics event key for a workflow.
func WorkflowAnalyticsEventKey(workflowID string) string {
	return fmt.Sprintf("analytics:workflow:%s", workflowID)
}

// LogEventKey returns the deterministic event key for a single job log line.
func LogEventKey(jobID, stream string, sequenceNum uint32) string {
	return fmt.Sprintf("log:%s:%s:%d", jobID, stream, sequenceNum)
}

// NotificationEventKey returns the deterministic idempotency key for a notification.
func NotificationEventKey(entity, entityID, eventType string) string {
	return fmt.Sprintf("notification:%s:%s:%s", entity, entityID, eventType)
}
