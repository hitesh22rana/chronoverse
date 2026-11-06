//nolint:testpackage // Pins persisted hashes and legacy response classification.
package workflows

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestWorkflowHashGoldenValuesAndAliasOrdering(t *testing.T) {
	t.Parallel()
	fields := map[string]any{
		"workflow_id": "550e8400-e29b-41d4-a716-446655440000", "user_id": "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", "name": "workflow",
		"payload": `{ "timeout": 5, "endpoint": "https://example.com" }`, "interval": int32(60), "max_consecutive_job_failures_allowed": int32(3),
	}
	set, err := workflowRequestHashSet(fields, map[string]string{
		"workflow_id": "550E8400-E29B-41D4-A716-446655440000", "user_id": "AAAAAAAA-AAAA-4AAA-8AAA-AAAAAAAAAAAA",
	})
	require.NoError(t, err)
	require.Equal(t, "91d5eefbf647c0581a301a44b20f068fb086940a9809a3aa0df07052ca935ba3", set.requestHash)
	require.Equal(t, "042381ce508f8549cb03e2ba974db26bfd37e5b911d9f61883da9d5a169a118f", set.canonicalLegacyHash)
	require.Equal(t, "165e9512d4dea06af7bbb31d9670df43cb2b2694c4645050dad20902c614c554", set.rawLegacyHash)
	require.Equal(t, []string{"042381ce508f8549cb03e2ba974db26bfd37e5b911d9f61883da9d5a169a118f", "165e9512d4dea06af7bbb31d9670df43cb2b2694c4645050dad20902c614c554"}, set.compatibleHashes)
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
		canonical, legacy, err := workflowRequestHashes(tc.fields)
		require.Equal(t, tc.code, status.Code(err))
		require.Empty(t, canonical)
		require.Empty(t, legacy)
		set, err := workflowRequestHashSet(tc.fields, nil)
		require.Equal(t, tc.code, status.Code(err))
		require.Equal(t, workflowRequestIdentitySet{}, set)
	}
}

func TestWorkflowHashAliasesRemoveDuplicatesWithoutReordering(t *testing.T) {
	t.Parallel()
	input := []string{"canonical", "canonical", "raw", "canonical", "raw"}
	require.Equal(t, []string{"canonical", "raw"}, uniqueRequestHashes(input))
	require.Equal(t, []string{"canonical", "canonical", "raw", "canonical", "raw"}, input)
	require.Empty(t, uniqueRequestHashes(nil))
}

func TestLegacyCreateResponseRecognizesOnlySingleIdentityField(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		raw    string
		legacy bool
	}{
		{"", false},
		{"{", false},
		{"null", false},
		{"[]", false},
		{`{"id":"workflow"}`, true},
		{`{"ID":"workflow"}`, true},
		{`{"name":"workflow"}`, false},
		{`{"ID":"workflow","Name":"named"}`, false},
	} {
		require.Equal(t, tc.legacy, isLegacyWorkflowCreateResponse(json.RawMessage(tc.raw)), tc.raw)
	}
}
