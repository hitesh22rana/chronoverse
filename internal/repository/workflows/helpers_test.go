//nolint:testpackage // Tests the unexported stale-event helper without widening production API.
package workflows

import (
	"database/sql"
	"testing"

	workflowsmodel "github.com/hitesh22rana/chronoverse/internal/model/workflows"
)

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
