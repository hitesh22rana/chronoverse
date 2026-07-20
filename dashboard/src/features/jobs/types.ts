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
