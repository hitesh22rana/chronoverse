import type {
    UserAnalytics,
    WorkflowAnalytics,
    WorkflowAnalyticsSummary,
    WorkflowKindAnalytics,
} from "@/features/analytics/types"

type WorkflowKindAnalyticsPayload = Partial<WorkflowKindAnalytics> | null
type WorkflowAnalyticsSummaryPayload = Partial<WorkflowAnalyticsSummary> | null

export type UserAnalyticsPayload = Partial<
    Omit<UserAnalytics, "workflow_kinds" | "top_workflows">
> & {
    workflow_kinds?: WorkflowKindAnalyticsPayload[] | null
    top_workflows?: WorkflowAnalyticsSummaryPayload[] | null
}

export type WorkflowAnalyticsPayload = Partial<WorkflowAnalytics>

const nonNegativeNumber = (value: unknown): number =>
    typeof value === "number" && Number.isFinite(value) && value >= 0 ? value : 0

const nonEmptyString = (value: unknown, fallback = ""): string => {
    if (typeof value !== "string") return fallback

    const normalized = value.trim()
    return normalized || fallback
}

const normalizeWorkflowKind = (
    item: WorkflowKindAnalyticsPayload,
): WorkflowKindAnalytics | null => {
    if (!item) return null

    const kind = nonEmptyString(item.kind)
    if (!kind) return null

    return {
        kind,
        total_workflows: nonNegativeNumber(item.total_workflows),
        total_jobs: nonNegativeNumber(item.total_jobs),
        total_joblogs: nonNegativeNumber(item.total_joblogs),
        total_job_execution_duration: nonNegativeNumber(item.total_job_execution_duration),
    }
}

const normalizeWorkflowSummary = (
    item: WorkflowAnalyticsSummaryPayload,
): WorkflowAnalyticsSummary | null => {
    if (!item) return null

    const workflowId = nonEmptyString(item.workflow_id)
    if (!workflowId) return null

    return {
        workflow_id: workflowId,
        workflow_name: nonEmptyString(item.workflow_name, "Deleted workflow"),
        kind: nonEmptyString(item.kind, "UNKNOWN"),
        total_jobs: nonNegativeNumber(item.total_jobs),
        total_joblogs: nonNegativeNumber(item.total_joblogs),
        total_job_execution_duration: nonNegativeNumber(item.total_job_execution_duration),
    }
}

export function normalizeUserAnalytics(payload?: UserAnalyticsPayload | null): UserAnalytics {
    return {
        total_workflows: nonNegativeNumber(payload?.total_workflows),
        total_jobs: nonNegativeNumber(payload?.total_jobs),
        total_joblogs: nonNegativeNumber(payload?.total_joblogs),
        total_job_execution_duration: nonNegativeNumber(payload?.total_job_execution_duration),
        workflow_kinds: (payload?.workflow_kinds ?? [])
            .map(normalizeWorkflowKind)
            .filter((item): item is WorkflowKindAnalytics => item !== null),
        top_workflows: (payload?.top_workflows ?? [])
            .map(normalizeWorkflowSummary)
            .filter((item): item is WorkflowAnalyticsSummary => item !== null),
    }
}

export function normalizeWorkflowAnalytics(
    payload: WorkflowAnalyticsPayload | null | undefined,
    workflowId: string,
): WorkflowAnalytics {
    return {
        workflow_id: nonEmptyString(payload?.workflow_id, workflowId),
        total_jobs: nonNegativeNumber(payload?.total_jobs),
        total_joblogs: nonNegativeNumber(payload?.total_joblogs),
        total_job_execution_duration: nonNegativeNumber(payload?.total_job_execution_duration),
    }
}
