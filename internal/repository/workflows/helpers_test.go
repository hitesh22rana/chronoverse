//nolint:testpackage // Tests the unexported stale-event helper without widening production API.
package workflows

import (
	"database/sql"
	"slices"
	"testing"

	workflowsmodel "github.com/hitesh22rana/chronoverse/internal/model/workflows"
)

func TestWorkflowRequestHashSetPreservesLegacyUUIDSpelling(t *testing.T) {
	t.Parallel()

	const (
		canonicalWorkflowID = "550e8400-e29b-41d4-a716-446655440000"
		rawWorkflowID       = "550E8400-E29B-41D4-A716-446655440000"
		userID              = "11111111-1111-4111-8111-111111111111"
	)
	canonicalFields := map[string]any{
		workflowRequestWorkflowIDField:                canonicalWorkflowID,
		workflowRequestUserIDField:                    userID,
		workflowRequestNameField:                      "workflow",
		workflowRequestPayloadField:                   `{"endpoint":"https://example.com"}`,
		workflowRequestIntervalField:                  int32(60),
		workflowRequestMaxConsecutiveJobFailuresField: int32(3),
	}
	rawFields := map[string]any{
		workflowRequestWorkflowIDField:                rawWorkflowID,
		workflowRequestUserIDField:                    userID,
		workflowRequestNameField:                      "workflow",
		workflowRequestPayloadField:                   `{"endpoint":"https://example.com"}`,
		workflowRequestIntervalField:                  int32(60),
		workflowRequestMaxConsecutiveJobFailuresField: int32(3),
	}

	requestHash, compatibleHashes, err := workflowRequestHashSet(canonicalFields, map[string]string{
		workflowRequestWorkflowIDField: rawWorkflowID,
		workflowRequestUserIDField:     userID,
	})
	if err != nil {
		t.Fatalf("workflowRequestHashSet() error = %v", err)
	}
	canonicalHash, _, err := workflowRequestHashes(canonicalFields)
	if err != nil {
		t.Fatalf("workflowRequestHashes() canonical error = %v", err)
	}
	_, rawLegacyHash, err := workflowRequestHashes(rawFields)
	if err != nil {
		t.Fatalf("workflowRequestHashes() raw error = %v", err)
	}
	if requestHash != canonicalHash {
		t.Fatalf("primary hash = %q, want canonical %q", requestHash, canonicalHash)
	}
	if !slices.Contains(compatibleHashes, rawLegacyHash) {
		t.Fatalf("compatible hashes %v do not contain legacy uppercase hash %q", compatibleHashes, rawLegacyHash)
	}
	if len(compatibleHashes) == 0 || compatibleHashes[0] != rawLegacyHash {
		t.Fatalf("first compatible hash = %v, want legacy uppercase hash %q", compatibleHashes, rawLegacyHash)
	}
}

func TestWorkflowRequestHashesPreserveLegacyPayloadCompatibility(t *testing.T) {
	t.Parallel()

	canonicalA, legacyA, err := workflowRequestHashes(map[string]any{"payload": `{ "image": "alpine" }`, "name": "workflow"})
	if err != nil {
		t.Fatalf("workflowRequestHashes() error = %v", err)
	}
	canonicalB, legacyB, err := workflowRequestHashes(map[string]any{"payload": `{"image":"alpine"}`, "name": "workflow"})
	if err != nil {
		t.Fatalf("workflowRequestHashes() canonical input error = %v", err)
	}
	if canonicalA != canonicalB {
		t.Fatalf("canonical hashes differ: %q != %q", canonicalA, canonicalB)
	}
	if legacyA == legacyB {
		t.Fatalf("legacy hashes unexpectedly match: %q", legacyA)
	}
}

func TestIsLegacyWorkflowCreateResponse(t *testing.T) {
	t.Parallel()

	if !isLegacyWorkflowCreateResponse([]byte(`{"id":"workflow-id"}`)) {
		t.Fatal("expected ID-only response to be classified as legacy")
	}
	if isLegacyWorkflowCreateResponse([]byte(`{"ID":"workflow-id","Name":"workflow"}`)) {
		t.Fatal("expected complete response not to be classified as legacy")
	}
}

func TestDecideWorkflowUpdateAction(t *testing.T) {
	t.Parallel()

	const (
		currentGeneration int64 = 3
		currentInterval   int32 = 5
		newInterval       int32 = 10
		buildHash               = "build-hash"
	)

	tests := []struct {
		name                           string
		currentBuildHash               sql.NullString
		currentBuildStatus             string
		reactivatingTerminatedWorkflow bool
		newBuildHash                   string
		newBuildHashValid              bool
		newInterval                    int32
		want                           updateWorkflowActionDecision
	}{
		{
			name: "terminated started workflow queues build",
			currentBuildHash: sql.NullString{
				String: buildHash,
				Valid:  true,
			},
			currentBuildStatus:             workflowsmodel.WorkflowBuildStatusStarted.ToString(),
			reactivatingTerminatedWorkflow: true,
			newBuildHash:                   buildHash,
			newBuildHashValid:              true,
			newInterval:                    currentInterval,
			want: updateWorkflowActionDecision{
				buildRequired:      true,
				rescheduleRequired: false,
				nextGeneration:     currentGeneration + 1,
				buildStatus:        workflowsmodel.WorkflowBuildStatusQueued.ToString(),
			},
		},
		{
			name: "terminated completed workflow queues build",
			currentBuildHash: sql.NullString{
				String: buildHash,
				Valid:  true,
			},
			currentBuildStatus:             workflowsmodel.WorkflowBuildStatusCompleted.ToString(),
			reactivatingTerminatedWorkflow: true,
			newBuildHash:                   buildHash,
			newBuildHashValid:              true,
			newInterval:                    currentInterval,
			want: updateWorkflowActionDecision{
				buildRequired:      true,
				rescheduleRequired: false,
				nextGeneration:     currentGeneration + 1,
				buildStatus:        workflowsmodel.WorkflowBuildStatusQueued.ToString(),
			},
		},
		{
			name: "active completed interval change reschedules",
			currentBuildHash: sql.NullString{
				String: buildHash,
				Valid:  true,
			},
			currentBuildStatus:             workflowsmodel.WorkflowBuildStatusCompleted.ToString(),
			reactivatingTerminatedWorkflow: false,
			newBuildHash:                   buildHash,
			newBuildHashValid:              true,
			newInterval:                    newInterval,
			want: updateWorkflowActionDecision{
				buildRequired:      false,
				rescheduleRequired: true,
				nextGeneration:     currentGeneration + 1,
			},
		},
		{
			name: "active started interval change waits for build completion",
			currentBuildHash: sql.NullString{
				String: buildHash,
				Valid:  true,
			},
			currentBuildStatus:             workflowsmodel.WorkflowBuildStatusStarted.ToString(),
			reactivatingTerminatedWorkflow: false,
			newBuildHash:                   buildHash,
			newBuildHashValid:              true,
			newInterval:                    newInterval,
			want: updateWorkflowActionDecision{
				buildRequired:      false,
				rescheduleRequired: false,
				nextGeneration:     currentGeneration,
			},
		},
		{
			name: "active build hash change queues build",
			currentBuildHash: sql.NullString{
				String: buildHash,
				Valid:  true,
			},
			currentBuildStatus:             workflowsmodel.WorkflowBuildStatusCompleted.ToString(),
			reactivatingTerminatedWorkflow: false,
			newBuildHash:                   "new-build-hash",
			newBuildHashValid:              true,
			newInterval:                    currentInterval,
			want: updateWorkflowActionDecision{
				buildRequired:      true,
				rescheduleRequired: false,
				nextGeneration:     currentGeneration + 1,
				buildStatus:        workflowsmodel.WorkflowBuildStatusQueued.ToString(),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := decideWorkflowUpdateAction(
				tt.currentBuildHash,
				currentGeneration,
				currentInterval,
				tt.currentBuildStatus,
				tt.reactivatingTerminatedWorkflow,
				tt.newBuildHash,
				tt.newBuildHashValid,
				tt.newInterval,
			); got != tt.want {
				t.Fatalf("decideWorkflowUpdateAction() = %+v, want %+v", got, tt.want)
			}
		})
	}
}
