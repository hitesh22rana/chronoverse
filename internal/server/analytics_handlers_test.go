//nolint:testpackage // Tests the unexported HTTP response adapters directly.
package server

import (
	"encoding/json"
	"testing"

	analyticspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/analytics"
)

func TestUserAnalyticsHTTPResponseEmitsZeroValues(t *testing.T) {
	t.Parallel()

	encoded, err := json.Marshal(newUserAnalyticsHTTPResponse(&analyticspb.GetUserAnalyticsResponse{}))
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}

	want := `{"total_workflows":0,"total_jobs":0,"total_joblogs":0,"total_job_execution_duration":0,"workflow_kinds":[],"top_workflows":[]}`
	if string(encoded) != want {
		t.Fatalf("got %s, want %s", encoded, want)
	}
}

func TestUserAnalyticsHTTPResponseEmitsNestedZeroValues(t *testing.T) {
	t.Parallel()

	response := newUserAnalyticsHTTPResponse(&analyticspb.GetUserAnalyticsResponse{
		WorkflowKinds: []*analyticspb.WorkflowKindAnalytics{{Kind: workflowKindContainer}},
		TopWorkflows: []*analyticspb.WorkflowAnalyticsSummary{{
			WorkflowId:   "workflow-id",
			WorkflowName: "Example",
			Kind:         workflowKindContainer,
		}},
	})

	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}

	want := `{"total_workflows":0,"total_jobs":0,"total_joblogs":0,"total_job_execution_duration":0,` +
		`"workflow_kinds":[{"kind":"CONTAINER","total_workflows":0,"total_jobs":0,"total_joblogs":0,` +
		`"total_job_execution_duration":0}],"top_workflows":[{"workflow_id":"workflow-id",` +
		`"workflow_name":"Example","kind":"CONTAINER","total_jobs":0,"total_joblogs":0,` +
		`"total_job_execution_duration":0}]}`
	if string(encoded) != want {
		t.Fatalf("got %s, want %s", encoded, want)
	}
}

func TestWorkflowAnalyticsHTTPResponseEmitsZeroValues(t *testing.T) {
	t.Parallel()

	encoded, err := json.Marshal(newWorkflowAnalyticsHTTPResponse(
		&analyticspb.GetWorkflowAnalyticsResponse{WorkflowId: "workflow-id"},
	))
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}

	want := `{"workflow_id":"workflow-id","total_job_execution_duration":0,"total_jobs":0,"total_joblogs":0}`
	if string(encoded) != want {
		t.Fatalf("got %s, want %s", encoded, want)
	}
}
