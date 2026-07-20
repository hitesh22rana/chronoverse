export const queryKeys = {
    user: ["user"] as const,
    userAnalytics: ["user-analytics"] as const,
    notifications: ["notifications"] as const,
    workflows: {
        all: ["workflows"] as const,
        list: (
            cursor: string,
            search: string,
            status: string,
            kind: string,
            intervalMin: string,
            intervalMax: string,
        ) => ["workflows", cursor, search, status, kind, intervalMin, intervalMax] as const,
    },
    workflow: {
        detail: (workflowId: string) => ["workflow", workflowId] as const,
        analytics: (workflowId: string) => ["workflow-analytics", workflowId] as const,
        jobs: (
            workflowId: string,
            cursor: string,
            status: string,
            trigger: string,
        ) => ["workflow-jobs", workflowId, cursor, status, trigger] as const,
    },
    job: {
        detail: (workflowId: string, jobId: string) => ["job-details", workflowId, jobId] as const,
        logs: (workflowId: string, jobId: string, status: string) =>
            ["job-logs", workflowId, jobId, status] as const,
        logSearch: (workflowId: string, jobId: string, search: string, stream: string) =>
            ["job-logs/search", workflowId, jobId, search, stream] as const,
    },
} as const
