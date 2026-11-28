"use client"

import { useQuery } from "@tanstack/react-query"
import { toast } from "sonner"

import { fetchApiJson } from "@/lib/api/client"
import { apiEndpoints } from "@/lib/api/endpoints"
import { queryRefetchIntervals, queryStaleTimes } from "@/lib/api/query-policy"
import { queryKeys } from "@/lib/api/query-keys"
import type { Job } from "@/features/jobs/types"

import { useWorkflowScope } from "@/features/workflows/workflow-freshness"

const refetchableJobStatus = ['PENDING', 'QUEUED', 'RUNNING']

export function useJobDetails(routeWorkflowId: string, routeJobId: string) {
    const workflowId = routeWorkflowId.toLowerCase()
    const jobId = routeJobId.toLowerCase()
    const scope = useWorkflowScope(workflowId)
    const getJobDetailsQuery = useQuery<Job, Error>({
        queryKey: scope.queryKey(queryKeys.job.detail(workflowId, jobId)),
        queryFn: ({ signal }) => {
            scope.requireCurrent()
            return fetchApiJson<Job>(apiEndpoints.workflows.jobs.detail(workflowId, jobId), "failed to fetch job details", { signal })
        },
        enabled: scope.available,
        refetchInterval: (query) => {
            const status = query.state.data?.status
            return status && refetchableJobStatus.includes(status)
                ? queryRefetchIntervals.activeJob
                : false
        },
        staleTime: queryStaleTimes.jobDetails,
    })

    if (scope.available && getJobDetailsQuery.error instanceof Error) {
        toast.error(getJobDetailsQuery.error.message)
    }

    return {
        job: (scope.available ? getJobDetailsQuery.data : undefined) as Job,
        isWorkflowAvailable: scope.available,
        isLoading: getJobDetailsQuery.isLoading,
        error: getJobDetailsQuery.error,
        refetch: () => scope.isCurrent() ? getJobDetailsQuery.refetch() : undefined,
    }
}
