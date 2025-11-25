"use client"

import { useCallback, useMemo } from "react"
import { usePathname, useRouter, useSearchParams } from "next/navigation"
import { useMutation, useQuery } from "@tanstack/react-query"
import { toast } from "sonner"

import { fetchWithAuth } from "@/lib/api-client"

const API_URL = process.env.NEXT_PUBLIC_API_URL
const WORKFLOW_JOBS_ENDPOINT = `${API_URL}/workflows`

export type Job = {
    id: string
    workflow_id: string
    status: string
    trigger: 'AUTOMATIC' | 'MANUAL'
    scheduled_at: string
    started_at?: string
    completed_at?: string
    created_at: string
    updated_at: string
}

export type JobsResponse = {
    jobs: Job[]
    cursor?: string
}

export function useWorkflowJobs(workflowId: string) {
    const router = useRouter()
    const path = usePathname()
    const searchParams = useSearchParams()

    const isNotWorkflowPath = path !== `/workflows/${workflowId}`

    // Get URL parameters
    let currentCursor = ""
    let statusFilter = ""
    let triggerFilter = ""

    if (isNotWorkflowPath) {
        currentCursor = ""
        statusFilter = ""
        triggerFilter = ""
    } else {
        currentCursor = searchParams.get("cursor") || ""
        statusFilter = searchParams.get("status") || ""
        triggerFilter = searchParams.get("trigger") || ""
    }

    // Build query parameters for the get jobs request
    const getJobQueryParams = useMemo(() => {
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
    }, [currentCursor, statusFilter, triggerFilter])

    const getJobQuery = useQuery({
        queryKey: ["workflow-jobs", workflowId, currentCursor, statusFilter, triggerFilter],
        queryFn: async () => {
            const url = getJobQueryParams ?
                `${WORKFLOW_JOBS_ENDPOINT}/${workflowId}/jobs?${getJobQueryParams}` :
                `${WORKFLOW_JOBS_ENDPOINT}/${workflowId}/jobs`

            const response = await fetchWithAuth(url)

            if (!response.ok) {
                throw new Error("Failed to fetch workflow jobs")
            }

            return response.json() as Promise<JobsResponse>
        },
        refetchInterval: 10000, // Refetch every 10 seconds
    })

    // Pagination functions
    const goToNextPage = useCallback(() => {
        const nextCursor = getJobQuery.data?.cursor
        if (!nextCursor) return false

        const params = new URLSearchParams(searchParams.toString())
        params.set("cursor", nextCursor)
        router.push(`?${params.toString()}`)
        return true
    }, [getJobQuery.data?.cursor, router, searchParams])

    const goToPreviousPage = useCallback(() => {
        router.back()
        return true
    }, [router])

    const resetPagination = useCallback(() => {
        const params = new URLSearchParams(searchParams.toString())
        params.delete("cursor")
        router.push(`?${params.toString()}`)
    }, [router, searchParams])

    // Apply all filters to the jobs
    const applyAllFilters = useCallback((filters: unknown) => {
        const params = new URLSearchParams(searchParams.toString())
        params.delete("cursor") // Reset pagination when applying filters

        const { status, trigger } = filters as { status?: string, trigger?: string }

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

        router.push(`?${params.toString()}`)
    }, [router, searchParams])

    // Clear all filters
    const clearAllFilters = useCallback(() => {
        const oldParams = new URLSearchParams(searchParams.toString())
        const tab = oldParams.get("tab")

        const params = new URLSearchParams()
        if (tab) {
            params.set("tab", tab)
        }

        router.push(`?${params.toString()}`)
    }, [router, searchParams])

    if (getJobQuery.error instanceof Error) {
        toast.error(getJobQuery.error.message)
    }

    const manualRunJobMutation = useMutation({
        mutationFn: async () => {
            const response = await fetchWithAuth(`${WORKFLOW_JOBS_ENDPOINT}/${workflowId}/jobs/schedule`, {
                method: "POST",
            })

            if (!response.ok) {
                throw new Error("Failed to schedule job")
            }
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
        manualRunJob: manualRunJobMutation.mutate,
        isManualRunJobPending: manualRunJobMutation.isPending,
        manualRunJobError: manualRunJobMutation.error,
    }
}
