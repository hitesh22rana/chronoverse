package analytics

import (
	analyticspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/analytics"
)

// GetUserAnalyticsResponse represents the response for user analytics.
type GetUserAnalyticsResponse struct {
	TotalWorkflows            uint32                      `db:"total_workflows"`
	TotalJobs                 uint64                      `db:"total_jobs"`
	TotalJoblogs              uint64                      `db:"total_joblogs"`
	TotalJobExecutionDuration uint64                      `db:"total_job_execution_duration"`
	WorkflowKinds             []*WorkflowKindAnalytics    `db:"-"`
	TopWorkflows              []*WorkflowAnalyticsSummary `db:"-"`
}

// WorkflowKindAnalytics represents durable analytics grouped by workflow kind.
type WorkflowKindAnalytics struct {
	Kind                      string `db:"kind"`
	TotalWorkflows            uint32 `db:"total_workflows"`
	TotalJobs                 uint64 `db:"total_jobs"`
	TotalJoblogs              uint64 `db:"total_joblogs"`
	TotalJobExecutionDuration uint64 `db:"total_job_execution_duration"`
}

// WorkflowAnalyticsSummary represents durable analytics for a single workflow.
type WorkflowAnalyticsSummary struct {
	WorkflowID                string `db:"workflow_id"`
	WorkflowName              string `db:"workflow_name"`
	Kind                      string `db:"kind"`
	TotalJobs                 uint64 `db:"total_jobs"`
	TotalJoblogs              uint64 `db:"total_joblogs"`
	TotalJobExecutionDuration uint64 `db:"total_job_execution_duration"`
}

// ToProto converts GetUserAnalyticsResponse to its protobuf representation.
func (r *GetUserAnalyticsResponse) ToProto() *analyticspb.GetUserAnalyticsResponse {
	workflowKinds := make([]*analyticspb.WorkflowKindAnalytics, 0, len(r.WorkflowKinds))
	for _, item := range r.WorkflowKinds {
		workflowKinds = append(workflowKinds, &analyticspb.WorkflowKindAnalytics{
			Kind:                      item.Kind,
			TotalWorkflows:            item.TotalWorkflows,
			TotalJobs:                 item.TotalJobs,
			TotalJoblogs:              item.TotalJoblogs,
			TotalJobExecutionDuration: item.TotalJobExecutionDuration,
		})
	}

	topWorkflows := make([]*analyticspb.WorkflowAnalyticsSummary, 0, len(r.TopWorkflows))
	for _, item := range r.TopWorkflows {
		topWorkflows = append(topWorkflows, &analyticspb.WorkflowAnalyticsSummary{
			WorkflowId:                item.WorkflowID,
			WorkflowName:              item.WorkflowName,
			Kind:                      item.Kind,
			TotalJobs:                 item.TotalJobs,
			TotalJoblogs:              item.TotalJoblogs,
			TotalJobExecutionDuration: item.TotalJobExecutionDuration,
		})
	}

	return &analyticspb.GetUserAnalyticsResponse{
		TotalWorkflows:            r.TotalWorkflows,
		TotalJobs:                 r.TotalJobs,
		TotalJoblogs:              r.TotalJoblogs,
		TotalJobExecutionDuration: r.TotalJobExecutionDuration,
		WorkflowKinds:             workflowKinds,
		TopWorkflows:              topWorkflows,
	}
}

// GetWorkflowAnalyticsResponse represents the response for workflow analytics.
type GetWorkflowAnalyticsResponse struct {
	WorkflowID                string `db:"workflow_id"`
	TotalJobExecutionDuration uint64 `db:"total_job_execution_duration"`
	TotalJobs                 uint32 `db:"total_jobs"`
	TotalJoblogs              uint64 `db:"total_joblogs"`
}

// ToProto converts GetWorkflowAnalyticsResponse to its protobuf representation.
func (r *GetWorkflowAnalyticsResponse) ToProto() *analyticspb.GetWorkflowAnalyticsResponse {
	return &analyticspb.GetWorkflowAnalyticsResponse{
		WorkflowId:                r.WorkflowID,
		TotalJobExecutionDuration: r.TotalJobExecutionDuration,
		TotalJobs:                 r.TotalJobs,
		TotalJoblogs:              r.TotalJoblogs,
	}
}
