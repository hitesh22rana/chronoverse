export type WorkflowAnalytics = {
    workflow_id: string
    total_job_execution_duration: number
    total_jobs: number
    total_joblogs: number
}

export type WorkflowKindAnalytics = {
    kind: string
    total_workflows: number
    total_jobs: number
    total_joblogs: number
    total_job_execution_duration: number
}

export type WorkflowAnalyticsSummary = {
    workflow_id: string
    workflow_name: string
    kind: string
    total_jobs: number
    total_joblogs: number
    total_job_execution_duration: number
}

export type UserAnalytics = {
    total_workflows: number
    total_jobs: number
    total_joblogs: number
    total_job_execution_duration: number
    workflow_kinds?: WorkflowKindAnalytics[]
    top_workflows?: WorkflowAnalyticsSummary[]
}
