"use client"

import { useCallback, useMemo } from "react"
import { usePathname, useRouter, useSearchParams } from "next/navigation"
import { useQuery } from "@tanstack/react-query"
import { toast } from "sonner"

import { fetchWithAuth } from "@/lib/api-client"

const API_URL = process.env.NEXT_PUBLIC_API_URL
const WORKFLOW_JOBS_ENDPOINT = `${API_URL}/workflows`

export type Job = {
    id: string
    workflow_id: string
    status: string
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
    let statusFilter = "ALL"

    if (isNotWorkflowPath) {
        currentCursor = ""
        statusFilter = "ALL"
    } else {
        currentCursor = searchParams.get("cursor") || ""
        statusFilter = searchParams.get("status") || "ALL"
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

        return params.toString()
    }, [currentCursor, statusFilter])

    const getJobQuery = useQuery({
        queryKey: ["workflow-jobs", workflowId, currentCursor, statusFilter],
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

        const { status } = filters as { status?: string }

        if (status && status !== "ALL") {
            params.set("status", status)
        } else {
            params.delete("status")
        }

        router.push(`?${params.toString()}`)
    }, [router, searchParams])

    // Handle errors
    if (getJobQuery.error instanceof Error) {
        toast.error(getJobQuery.error.message)
    }

    return {
        jobs: getJobQuery?.data?.jobs || [],
        isLoading: getJobQuery.isLoading,
        error: getJobQuery.error,
        refetch: getJobQuery.refetch,
        isRefetching: getJobQuery.isRefetching,
        applyAllFilters,
        pagination: {
            nextCursor: getJobQuery?.data?.cursor,
            hasNextPage: !!getJobQuery?.data?.cursor,
            hasPreviousPage: !!currentCursor,
            goToNextPage,
            goToPreviousPage,
            resetPagination,
            currentPage: currentCursor ? 'paginated' : 'first'
        }
    }
}