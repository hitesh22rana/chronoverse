export type User = {
    email: string
    notification_preference: "ALERTS" | "ALL" | "NONE"
    created_at: string
    updated_at: string
}

export type UpdateUserDetails = {
    notification_preference: string
}

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

export type Job = {
    id: string
    workflow_id: string
    status: "PENDING" | "QUEUED" | "RUNNING" | "COMPLETED" | "FAILED" | "CANCELED"
    trigger: "AUTOMATIC" | "MANUAL"
    scheduled_at: string
    started_at?: string
    completed_at?: string
    created_at: string
    updated_at: string
    status_reason_code?: string
    status_reason_message?: string
}

export type JobsResponse = {
    jobs: Job[]
    cursor?: string
}

export type Notification = {
    id: string
    kind: string
    payload: string
    read_at?: string
    created_at: string
    updated_at: string
}

export type NotificationPayload = {
    title: string
    message: string
    entity_id: string
    entity_type: string
    action_url: string
}

export type NotificationsResponse = {
    notifications: Notification[]
    cursor?: string
}

export type JobLog = {
    event_id?: string
    highlightToken?: string
    timestamp: string
    message: string
    sequence_num: number
    stream: "stdout" | "stderr"
}

export type JobLogsResponse = {
    id: string
    workflow_id: string
    logs: JobLog[]
    cursor?: string
    highlight_token?: string
}

export type DownloadLogsFormat = "txt" | "json" | "jsonl"
