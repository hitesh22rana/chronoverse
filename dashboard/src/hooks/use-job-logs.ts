"use client"

import { useInfiniteQuery } from "@tanstack/react-query"
import { fetchWithAuth } from "@/lib/api-client"
import { toast } from "sonner"
import { cleanLog } from "@/lib/utils"

const API_URL = process.env.NEXT_PUBLIC_API_URL

type JobLog = {
    timestamp: string
    message: string
    sequence_num: number
}

export type JobLogsResponseData = {
    id: string
    workflow_id: string
    logs: JobLog[]
    cursor?: string
}

export type JobLogsResponse = {
    id: string
    workflow_id: string
    logs: string[]
    cursor?: string
}

export function useJobLogs(workflowId: string, jobId: string, jobStatus: string) {
    const queryKey = ["job-logs", workflowId, jobId, jobStatus]

    const query = useInfiniteQuery<JobLogsResponse, Error>({
        queryKey,
        queryFn: async ({ pageParam }) => {
            const url = pageParam
                ? `${API_URL}/workflows/${workflowId}/jobs/${jobId}/logs?cursor=${pageParam}`
                : `${API_URL}/workflows/${workflowId}/jobs/${jobId}/logs`

            const response = await fetchWithAuth(url)

            if (!response.ok) {
                throw new Error("failed to fetch job logs")
            }

            const res = await response.json() as JobLogsResponseData

            if (!res.logs) {
                return {
                    id: jobId,
                    workflow_id: workflowId,
                    logs: [],
                    cursor: undefined,
                }
            }

            const data: JobLogsResponse = {
                id: res.id,
                workflow_id: res.workflow_id,
                logs: cleanLog(res.logs),
                cursor: res.cursor || undefined,
            }

            return data
        },
        initialPageParam: null,
        getNextPageParam: (lastPage) => lastPage?.cursor || null,
        refetchInterval: jobStatus === "RUNNING" ? 1000 : false, // Refetch every second if the job is running
        enabled: jobStatus !== "PENDING", // Only fetch logs if the job is not pending
    })

    if (query.error instanceof Error) {
        toast.error(query.error.message)
    }

    const allPages = query.data?.pages || []
    const logs = allPages.length > 0 ? allPages.flatMap((page) => page?.logs || []) : []

    return {
        logs,
        isLoading: query.isLoading,
        error: query.error,
        fetchNextPage: query.fetchNextPage,
        isFetchingNextPage: query.isFetchingNextPage,
        hasNextPage: query.hasNextPage,
        refetch: query.refetch,
    }
}