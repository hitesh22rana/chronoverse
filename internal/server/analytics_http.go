package server

import analyticspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/analytics"

type userAnalyticsHTTPResponse struct {
	TotalWorkflows            uint32                                 `json:"total_workflows"`
	TotalJobs                 uint64                                 `json:"total_jobs"`
	TotalJoblogs              uint64                                 `json:"total_joblogs"`
	TotalJobExecutionDuration uint64                                 `json:"total_job_execution_duration"`
	WorkflowKinds             []workflowKindAnalyticsHTTPResponse    `json:"workflow_kinds"`
	TopWorkflows              []workflowAnalyticsSummaryHTTPResponse `json:"top_workflows"`
}

type workflowKindAnalyticsHTTPResponse struct {
	Kind                      string `json:"kind"`
	TotalWorkflows            uint32 `json:"total_workflows"`
	TotalJobs                 uint64 `json:"total_jobs"`
	TotalJoblogs              uint64 `json:"total_joblogs"`
	TotalJobExecutionDuration uint64 `json:"total_job_execution_duration"`
}

type workflowAnalyticsSummaryHTTPResponse struct {
	WorkflowID                string `json:"workflow_id"`
	WorkflowName              string `json:"workflow_name"`
	Kind                      string `json:"kind"`
	TotalJobs                 uint64 `json:"total_jobs"`
	TotalJoblogs              uint64 `json:"total_joblogs"`
	TotalJobExecutionDuration uint64 `json:"total_job_execution_duration"`
}

type workflowAnalyticsHTTPResponse struct {
	WorkflowID                string `json:"workflow_id"`
	TotalJobExecutionDuration uint64 `json:"total_job_execution_duration"`
	TotalJobs                 uint32 `json:"total_jobs"`
	TotalJoblogs              uint64 `json:"total_joblogs"`
}

func toUserAnalyticsHTTPResponse(res *analyticspb.GetUserAnalyticsResponse) userAnalyticsHTTPResponse {
	workflowKinds := make([]workflowKindAnalyticsHTTPResponse, 0, len(res.GetWorkflowKinds()))
	for _, item := range res.GetWorkflowKinds() {
		workflowKinds = append(workflowKinds, workflowKindAnalyticsHTTPResponse{
			Kind:                      item.GetKind(),
			TotalWorkflows:            item.GetTotalWorkflows(),
			TotalJobs:                 item.GetTotalJobs(),
			TotalJoblogs:              item.GetTotalJoblogs(),
			TotalJobExecutionDuration: item.GetTotalJobExecutionDuration(),
		})
	}

	topWorkflows := make([]workflowAnalyticsSummaryHTTPResponse, 0, len(res.GetTopWorkflows()))
	for _, item := range res.GetTopWorkflows() {
		topWorkflows = append(topWorkflows, workflowAnalyticsSummaryHTTPResponse{
			WorkflowID:                item.GetWorkflowId(),
			WorkflowName:              item.GetWorkflowName(),
			Kind:                      item.GetKind(),
			TotalJobs:                 item.GetTotalJobs(),
			TotalJoblogs:              item.GetTotalJoblogs(),
			TotalJobExecutionDuration: item.GetTotalJobExecutionDuration(),
		})
	}

	return userAnalyticsHTTPResponse{
		TotalWorkflows:            res.GetTotalWorkflows(),
		TotalJobs:                 res.GetTotalJobs(),
		TotalJoblogs:              res.GetTotalJoblogs(),
		TotalJobExecutionDuration: res.GetTotalJobExecutionDuration(),
		WorkflowKinds:             workflowKinds,
		TopWorkflows:              topWorkflows,
	}
}

func toWorkflowAnalyticsHTTPResponse(res *analyticspb.GetWorkflowAnalyticsResponse) workflowAnalyticsHTTPResponse {
	return workflowAnalyticsHTTPResponse{
		WorkflowID:                res.GetWorkflowId(),
		TotalJobExecutionDuration: res.GetTotalJobExecutionDuration(),
		TotalJobs:                 res.GetTotalJobs(),
		TotalJoblogs:              res.GetTotalJoblogs(),
	}
}
