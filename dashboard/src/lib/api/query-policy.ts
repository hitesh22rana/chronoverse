const SECOND = 1000
const MINUTE = 60 * SECOND

/**
 * Freshness windows prevent refetch-on-mount while cached data is still fresh.
 * Explicit refetch intervals continue to run independently of these values.
 */
export const queryStaleTimes = {
    user: 5 * MINUTE,
    analytics: 5 * MINUTE,
    workflowList: 30 * SECOND,
    workflowDetails: 30 * SECOND,
    workflowJobs: 10 * SECOND,
    jobDetails: 30 * SECOND,
} as const

/**
 * Polling cadence is query-specific so active resources update quickly without
 * applying the same request rate to idle or background data.
 */
export const queryRefetchIntervals = {
    activeWorkflowBuild: 5 * SECOND,
    activeJob: 5 * SECOND,
    workflowJobs: 10 * SECOND,
    idleWorkflowList: MINUTE,
    notifications: MINUTE,
} as const
