"use client"

import { usePathname, useRouter, useSearchParams } from "next/navigation"
import { useMutation, useQuery } from "@tanstack/react-query"
import { toast } from "sonner"

import { createIdempotencyKey, fetchApi, fetchApiJson } from "@/lib/api/client"
import { apiEndpoints, withQuery } from "@/lib/api/endpoints"
import { queryRefetchIntervals, queryStaleTimes } from "@/lib/api/query-policy"
import { queryKeys } from "@/lib/api/query-keys"
import type { JobsResponse } from "@/features/jobs/types"

type UseWorkflowJobsOptions = {
    enabled?: boolean
}

type SearchParams = Pick<URLSearchParams, "get" | "toString">
type JobFilters = { status?: string; trigger?: string }

function readJobFilters(path: string, workflowId: string, searchParams: SearchParams) {
    const isJobsPage = path === `/workflows/${workflowId}` && searchParams.get("tab") === "jobs"
    const params = isJobsPage ? searchParams : new URLSearchParams()
    return {
        currentCursor: params.get("cursor") || "",
        statusFilter: params.get("status") || "",
        triggerFilter: params.get("trigger") || "",
    }
}

function jobQueryParams({ currentCursor, statusFilter, triggerFilter }: ReturnType<typeof readJobFilters>) {
    const params = new URLSearchParams()

    if (currentCursor) {
        params.set("cursor", currentCursor)
    }

    if (statusFilter && statusFilter !== "ALL") {
        params.set("status", statusFilter)
    }

    if (triggerFilter && triggerFilter !== "ALL") {
        params.set("trigger", triggerFilter)
    }

    return params.toString()
}

function applyJobFilters(params: URLSearchParams, filters: JobFilters) {
    const { status, trigger } = filters

    if (status && status !== "ALL") {
        params.set("status", status)
    } else {
        params.delete("status")
    }

    if (trigger && trigger !== "ALL") {
        params.set("trigger", trigger)
    } else {
        params.delete("trigger")
    }
}

export function useWorkflowJobs(
    workflowId: string,
    { enabled = true }: UseWorkflowJobsOptions = {},
) {
    const router = useRouter()
    const path = usePathname()
    const searchParams = useSearchParams()

    const filters = readJobFilters(path, workflowId, searchParams)
    const { currentCursor, statusFilter, triggerFilter } = filters
    const getJobQueryParams = jobQueryParams(filters)

    const getJobQuery = useQuery({
        queryKey: queryKeys.workflow.jobs(
            workflowId,
            currentCursor,
            statusFilter,
            triggerFilter,
        ),
        queryFn: () => fetchApiJson<JobsResponse>(
            withQuery(apiEndpoints.workflows.jobs.list(workflowId), getJobQueryParams),
            "Failed to fetch workflow jobs",
        ),
        enabled: enabled && !!workflowId,
        refetchInterval: queryRefetchIntervals.workflowJobs,
        staleTime: queryStaleTimes.workflowJobs,
    })

    const goToNextPage = () => {
        const nextCursor = getJobQuery.data?.cursor
        if (!nextCursor) return false

        const params = new URLSearchParams(searchParams.toString())
        params.set("cursor", nextCursor)
        router.push(`?${params.toString()}`)
        return true
    }

    const goToPreviousPage = () => {
        router.back()
        return true
    }

    const resetPagination = () => {
        const params = new URLSearchParams(searchParams.toString())
        params.delete("cursor")
        router.push(`?${params.toString()}`)
    }

    const applyAllFilters = (filters: unknown) => {
        const params = new URLSearchParams(searchParams.toString())
        params.delete("cursor") // Reset pagination when applying filters

        applyJobFilters(params, filters as JobFilters)

        router.push(`?${params.toString()}`)
    }

    const clearAllFilters = () => {
        const oldParams = new URLSearchParams(searchParams.toString())
        const tab = oldParams.get("tab")

        const params = new URLSearchParams()
        if (tab) {
            params.set("tab", tab)
        }

        router.push(`?${params.toString()}`)
    }

    if (getJobQuery.error instanceof Error) {
        toast.error(getJobQuery.error.message)
    }

    const manualRunJobMutation = useMutation({
        mutationFn: async (idempotencyKey: string) => {
            await fetchApi(apiEndpoints.workflows.jobs.schedule(workflowId), "Failed to schedule job", {
                method: "POST",
                headers: {
                    "Idempotency-Key": idempotencyKey,
                },
            })
        },
        onSuccess: () => {
            toast.success("Job scheduled successfully")
            getJobQuery.refetch()
        },
        onError: (error) => {
            toast.error(error.message)
        }
    })

    return {
        jobs: getJobQuery?.data?.jobs || [],
        isLoading: getJobQuery.isLoading,
        error: getJobQuery.error,
        refetch: getJobQuery.refetch,
        isRefetching: getJobQuery.isRefetching,
        statusFilter,
        triggerFilter,
        applyAllFilters,
        clearAllFilters,
        pagination: {
            nextCursor: getJobQuery?.data?.cursor,
            hasNextPage: !!getJobQuery?.data?.cursor,
            hasPreviousPage: !!currentCursor,
            goToNextPage,
            goToPreviousPage,
            resetPagination,
            currentPage: currentCursor ? 'paginated' : 'first'
        },
        manualRunJob: () => manualRunJobMutation.mutate(createIdempotencyKey()),
        isManualRunJobPending: manualRunJobMutation.isPending,
        manualRunJobError: manualRunJobMutation.error,
    }
}
