//nolint:testpackage // Tests the unexported stale-event helper without widening production API.
package workflow

import (
	"testing"

	workflowspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/workflows"

	workflowsmodel "github.com/hitesh22rana/chronoverse/internal/model/workflows"
)

func TestIsStaleWorkflowEvent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		current       int64
		event         int64
		expectedStale bool
	}{
		{
			name:          "current generation",
			current:       3,
			event:         3,
			expectedStale: false,
		},
		{
			name:          "stale generation",
			current:       3,
			event:         2,
			expectedStale: true,
		},
		{
			name:          "future generation",
			current:       3,
			event:         4,
			expectedStale: true,
		},
		{
			name:          "legacy event",
			current:       3,
			event:         0,
			expectedStale: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			workflow := &workflowspb.GetWorkflowByIDResponse{
				Generation: tt.current,
			}
			event := workflowsmodel.WorkflowEvent{
				Generation: tt.event,
			}

			if got := isStaleWorkflowEvent(workflow, event); got != tt.expectedStale {
				t.Fatalf("isStaleWorkflowEvent() = %v, want %v", got, tt.expectedStale)
			}
		})
	}
}
