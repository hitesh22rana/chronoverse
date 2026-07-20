"use client"

import { useQuery } from "@tanstack/react-query"
import { toast } from "sonner"

import { fetchApiJson } from "@/lib/api/client"
import { apiEndpoints } from "@/lib/api/endpoints"
import { queryKeys } from "@/lib/api/query-keys"
import type { Job } from "@/lib/api/types"

const refetchableJobStatus = ['PENDING', 'QUEUED', 'RUNNING']

export function useJobDetails(workflowId: string, jobId: string) {
    const getJobDetailsQuery = useQuery<Job, Error>({
        queryKey: queryKeys.job.detail(workflowId, jobId),
        queryFn: () => fetchApiJson<Job>(
            apiEndpoints.workflows.jobs.detail(workflowId, jobId),
            "failed to fetch job details",
        ),
        refetchInterval: (query) => {
            const status = query.state.data?.status
            return status && refetchableJobStatus.includes(status) ? 5000 : false
        },
    })

    if (getJobDetailsQuery.error instanceof Error) {
        toast.error(getJobDetailsQuery.error.message)
    }

    return {
        job: getJobDetailsQuery.data as Job,
        isLoading: getJobDetailsQuery.isLoading,
        error: getJobDetailsQuery.error,
        refetch: getJobDetailsQuery.refetch,
    }
}
