package analytics

import (
	analyticspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/analytics"
)

// GetUserAnalyticsResponse represents the response for user analytics.
type GetUserAnalyticsResponse struct {
	TotalWorkflows            uint32 `db:"total_workflows"`
	TotalJobs                 uint64 `db:"total_jobs"`
	TotalJoblogs              uint64 `db:"total_joblogs"`
	TotalJobExecutionDuration uint64 `db:"total_job_execution_duration"`
}

// ToProto converts GetUserAnalyticsResponse to its protobuf representation.
func (r *GetUserAnalyticsResponse) ToProto() *analyticspb.GetUserAnalyticsResponse {
	return &analyticspb.GetUserAnalyticsResponse{
		TotalWorkflows:            r.TotalWorkflows,
		TotalJobs:                 r.TotalJobs,
		TotalJoblogs:              r.TotalJoblogs,
		TotalJobExecutionDuration: r.TotalJobExecutionDuration,
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
