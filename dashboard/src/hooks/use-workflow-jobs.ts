"use client"

import { useCallback, useMemo } from "react"
import { usePathname, useRouter, useSearchParams } from "next/navigation"
import { useQuery } from "@tanstack/react-query"
import { toast } from "sonner"

import { fetchWithAuth } from "@/utils/api-client"

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

    const query = useQuery({
        queryKey: ["workflow-jobs", workflowId, currentCursor, statusFilter],
        queryFn: async () => {
            const url = currentCursor ?
                `${WORKFLOW_JOBS_ENDPOINT}/${workflowId}/jobs?cursor=${currentCursor}` :
                `${WORKFLOW_JOBS_ENDPOINT}/${workflowId}/jobs`

            const response = await fetchWithAuth(url)

            if (!response.ok) {
                throw new Error("Failed to fetch workflow jobs")
            }

            return response.json() as Promise<JobsResponse>
        },
    })

    // Pagination functions
    const goToNextPage = useCallback(() => {
        const nextCursor = query.data?.cursor
        if (!nextCursor) return false

        const params = new URLSearchParams(searchParams.toString())
        params.set("cursor", nextCursor)
        router.push(`?${params.toString()}`)
        return true
    }, [query.data?.cursor, router, searchParams])

    const goToPreviousPage = useCallback(() => {
        router.back()
        return true
    }, [router])

    const resetPagination = useCallback(() => {
        const params = new URLSearchParams(searchParams.toString())
        params.delete("cursor")
        router.push(`?${params.toString()}`)
    }, [router, searchParams])

    // Apply status filter client-side (similar to useWorkflows)
    const filteredJobs = useMemo(() => {
        if (!query.data?.jobs) return []

        // If statusFilter is ALL, return all jobs
        if (statusFilter === "ALL") {
            return query.data.jobs
        }

        // Otherwise filter by status
        return query.data.jobs.filter(job => job.status === statusFilter)
    }, [query.data?.jobs, statusFilter])

    // Handle errors
    if (query.error instanceof Error) {
        toast.error(query.error.message)
    }

    return {
        jobs: filteredJobs,
        isLoading: query.isLoading,
        error: query.error,
        refetch: query.refetch,
        isRefetching: query.isRefetching,
        pagination: {
            nextCursor: query.data?.cursor,
            hasNextPage: !!query.data?.cursor,
            hasPreviousPage: !!currentCursor,
            goToNextPage,
            goToPreviousPage,
            resetPagination,
            currentPage: currentCursor ? 'paginated' : 'first'
        }
    }
}