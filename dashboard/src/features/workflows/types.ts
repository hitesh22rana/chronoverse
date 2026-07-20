export type Workflow = {
    id: string
    name: string
    kind: string
    payload: string
    build_status: string
    interval: number
    consecutive_job_failures_count?: number
    max_consecutive_job_failures_allowed: number
    log_retention: boolean
    created_at: string
    updated_at: string
    terminated_at?: string
}

export type WorkflowsResponse = {
    workflows: Workflow[]
    cursor?: string
}

export type CreateWorkflowPayload = {
    name: string
    kind: string
    payload: string
    interval: number
    max_consecutive_job_failures_allowed: number
    log_retention: boolean
}

export type UpdateWorkflowDetails = {
    name: string
    payload: string
    interval: number
    max_consecutive_job_failures_allowed: number
}
