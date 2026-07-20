const normalizeBaseUrl = (baseUrl?: string) => (baseUrl ?? "").trim().replace(/\/+$/, "")

const encodePathSegment = (value: string) => encodeURIComponent(value)

export function createApiEndpoints(baseUrl?: string) {
    const apiBaseUrl = normalizeBaseUrl(baseUrl)
    const route = (path: string) => `${apiBaseUrl}${path}`
    const workflow = (workflowId: string) => route(`/workflows/${encodePathSegment(workflowId)}`)
    const job = (workflowId: string, jobId: string) =>
        `${workflow(workflowId)}/jobs/${encodePathSegment(jobId)}`

    return {
        auth: {
            login: route("/auth/login"),
            signup: route("/auth/register"),
            logout: route("/auth/logout"),
        },
        users: route("/users"),
        notifications: route("/notifications"),
        analytics: {
            user: route("/analytics"),
            workflow: (workflowId: string) => route(`/analytics/${encodePathSegment(workflowId)}`),
        },
        workflows: {
            list: route("/workflows"),
            detail: workflow,
            jobs: {
                list: (workflowId: string) => `${workflow(workflowId)}/jobs`,
                schedule: (workflowId: string) => `${workflow(workflowId)}/jobs/schedule`,
                detail: job,
                logs: (workflowId: string, jobId: string) => `${job(workflowId, jobId)}/logs`,
                logEvents: (workflowId: string, jobId: string) => `${job(workflowId, jobId)}/events`,
                rawLogs: (workflowId: string, jobId: string) => `${job(workflowId, jobId)}/logs/raw`,
                searchLogs: (workflowId: string, jobId: string) => `${job(workflowId, jobId)}/logs/search`,
            },
        },
    } as const
}

export function withQuery(url: string, query?: string | URLSearchParams) {
    const search = typeof query === "string" ? query : query?.toString()
    return search ? `${url}?${search}` : url
}

export const apiEndpoints = createApiEndpoints(process.env.NEXT_PUBLIC_API_URL)
