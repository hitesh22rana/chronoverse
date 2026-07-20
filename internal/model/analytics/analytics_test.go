package analytics_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

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

	assert.Equal(t, uint32(19), response.GetTotalWorkflows())
	assert.Equal(t, uint64(286), response.GetTotalJobs())
	assert.Equal(t, "CONTAINER", response.GetWorkflowKinds()[0].GetKind())
	assert.Equal(t, uint64(219), response.GetWorkflowKinds()[0].GetTotalJobs())
	assert.Equal(t, "daily-report", response.GetTopWorkflows()[0].GetWorkflowName())
	assert.Equal(t, uint64(42), response.GetTopWorkflows()[0].GetTotalJobs())
}
