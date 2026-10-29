package analytics_test

import (
	"testing"

	"google.golang.org/protobuf/proto"

	analyticspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/analytics"

	analyticsmodel "github.com/hitesh22rana/chronoverse/internal/model/analytics"
)

func TestGetUserAnalyticsResponseToProto(t *testing.T) {
	response := (&analyticsmodel.GetUserAnalyticsResponse{
		TotalWorkflows:            19,
		TotalJobs:                 286,
		TotalJoblogs:              26056,
		TotalJobExecutionDuration: 5225,
		WorkflowKinds: []*analyticsmodel.WorkflowKindAnalytics{
			{
				Kind:                      "CONTAINER",
				TotalWorkflows:            17,
				TotalJobs:                 219,
				TotalJoblogs:              26056,
				TotalJobExecutionDuration: 5222,
			},
		},
		TopWorkflows: []*analyticsmodel.WorkflowAnalyticsSummary{
			{
				WorkflowID:                "workflow-1",
				WorkflowName:              "daily-report",
				Kind:                      "CONTAINER",
				TotalJobs:                 42,
				TotalJoblogs:              1200,
				TotalJobExecutionDuration: 900,
			},
		},
	}).ToProto()

	want := &analyticspb.GetUserAnalyticsResponse{
		TotalWorkflows: 19, TotalJobs: 286, TotalJoblogs: 26056, TotalJobExecutionDuration: 5225,
		WorkflowKinds: []*analyticspb.WorkflowKindAnalytics{{
			Kind: "CONTAINER", TotalWorkflows: 17, TotalJobs: 219, TotalJoblogs: 26056, TotalJobExecutionDuration: 5222,
		}},
		TopWorkflows: []*analyticspb.WorkflowAnalyticsSummary{{
			WorkflowId: "workflow-1", WorkflowName: "daily-report", Kind: "CONTAINER",
			TotalJobs: 42, TotalJoblogs: 1200, TotalJobExecutionDuration: 900,
		}},
	}
	if !proto.Equal(response, want) {
		t.Fatalf("ToProto = %v, want %v", response, want)
	}
}
