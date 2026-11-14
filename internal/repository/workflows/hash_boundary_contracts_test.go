//nolint:testpackage // Pins canonical persisted workflow hashes.
package workflows

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestWorkflowHashGoldenValue(t *testing.T) {
	t.Parallel()
	fields := map[string]any{
		"workflow_id": "550e8400-e29b-41d4-a716-446655440000", "user_id": "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", "name": "workflow",
		"payload": `{ "timeout": 5, "endpoint": "https://example.com" }`, "interval": int32(60), "max_consecutive_job_failures_allowed": int32(3),
	}
	hash, err := workflowRequestHash(fields)
	require.NoError(t, err)
	require.Equal(t, "91d5eefbf647c0581a301a44b20f068fb086940a9809a3aa0df07052ca935ba3", hash)
	require.Equal(t, `{ "timeout": 5, "endpoint": "https://example.com" }`, fields["payload"])
	require.Equal(t, "550e8400-e29b-41d4-a716-446655440000", fields["workflow_id"])
}

func TestWorkflowHashRejectsInvalidPayloadAndUnserializableScalar(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		fields map[string]any
		code   codes.Code
	}{
		{map[string]any{"payload": "{"}, codes.InvalidArgument},
		{map[string]any{"interval": math.Inf(1)}, codes.Internal},
	} {
		canonical, err := workflowRequestHash(tc.fields)
		require.Equal(t, tc.code, status.Code(err))
		require.Empty(t, canonical)
	}
}
