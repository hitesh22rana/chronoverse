"use client"

import { useCallback, useMemo } from "react"
import { usePathname, useRouter, useSearchParams } from "next/navigation"
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"

import { fetchWithAuth } from "@/lib/api-client"

const API_URL = process.env.NEXT_PUBLIC_API_URL
const WORKFLOWS_ENDPOINT = `${API_URL}/workflows`

export type WorkflowsResponse = {
    workflows: Workflow[]
    cursor?: string
}

export type Workflow = {
    id: string
    name: string
    kind: string
    payload: string
    build_status: string
    interval: number
    consecutive_job_failures_count?: number
    max_consecutive_job_failures_allowed: number
    created_at: string
    updated_at: string
    terminated_at?: string
}

export function useWorkflows() {
    const queryClient = useQueryClient()
    const router = useRouter()
    const path = usePathname()
    const searchParams = useSearchParams()

    const isNotRootPath = path !== "/"

    let currentCursor = ""
    let searchQuery = ""
    let statusFilter = ""
    let kindFilter = ""
    let intervalMin = ""
    let intervalMax = ""
    let createdAfter = ""
    let createdBefore = ""

    if (isNotRootPath) {
        currentCursor = ""
        searchQuery = ""
        statusFilter = ""
        kindFilter = ""
        intervalMin = ""
        intervalMax = ""
        createdAfter = ""
        createdBefore = ""
    } else {
        currentCursor = searchParams.get("cursor") || ""
        searchQuery = searchParams.get("query") || ""
        statusFilter = searchParams.get("status") || ""
        kindFilter = searchParams.get("kind") || ""
        intervalMin = searchParams.get("interval_min") || ""
        intervalMax = searchParams.get("interval_max") || ""
        createdAfter = searchParams.get("created_after") || ""
        createdBefore = searchParams.get("created_before") || ""
    }

    // Build query parameters for the API call
    const queryParams = useMemo(() => {
        const params = new URLSearchParams()

        if (currentCursor) {
            params.set("cursor", currentCursor)
        }

        if (searchQuery) {
            params.set("query", searchQuery)
        }

        if (statusFilter) {
            if (statusFilter === "TERMINATED") {
                params.set("terminated", "true")
            } else {
                params.set("build_status", statusFilter)
            }
        }

        if (kindFilter) {
            params.set("kind", kindFilter)
        }

        if (intervalMin) {
            params.set("interval_min", intervalMin)
        }

        if (intervalMax) {
            params.set("interval_max", intervalMax)
        }

        if (createdAfter) {
            params.set("created_after", createdAfter)
        }

        if (createdBefore) {
            params.set("created_before", createdBefore)
        }

        return params.toString()
    }, [currentCursor, searchQuery, statusFilter, kindFilter, intervalMin, intervalMax, createdAfter, createdBefore])

    const query = useQuery({
        queryKey: ["workflows", currentCursor, searchQuery, statusFilter, kindFilter, intervalMin, intervalMax, createdAfter, createdBefore],
        queryFn: async () => {
            const url = queryParams
                ? `${WORKFLOWS_ENDPOINT}?${queryParams}`
                : WORKFLOWS_ENDPOINT

            const response = await fetchWithAuth(url)

            if (!response.ok) {
                throw new Error("failed to fetch workflows")
            }

            return response.json() as Promise<WorkflowsResponse>
        },
        refetchInterval: 10000,
    })

    const goToNextPage = useCallback(() => {
        const nextCursor = query?.data?.cursor
        if (!nextCursor) return false

        const params = new URLSearchParams(searchParams.toString())
        params.set("cursor", nextCursor)
        router.push(`?${params.toString()}`)
        return true
    }, [query?.data?.cursor, router, searchParams])

    const goToPreviousPage = useCallback(() => {
        router.back()
        return true
    }, [router])

    const resetPagination = useCallback(() => {
        const params = new URLSearchParams(searchParams.toString())
        params.delete("cursor")
        router.push(`?${params.toString()}`)
    }, [router, searchParams])

    // Update search query in URL params
    const updateSearchQuery = useCallback((newQuery: string) => {
        const params = new URLSearchParams(searchParams.toString())
        params.delete("cursor") // Reset pagination when searching

        if (newQuery) {
            params.set("query", newQuery)
        } else {
            params.delete("query")
        }

        router.push(`?${params.toString()}`)
    }, [router, searchParams])

    // Update status filter in URL params
    const updateStatusFilter = useCallback((newStatus: string) => {
        const params = new URLSearchParams(searchParams.toString())
        params.delete("cursor") // Reset pagination when filtering

        if (newStatus && newStatus !== "ALL") {
            params.set("status", newStatus)
        } else {
            params.delete("status")
        }

        router.push(`?${params.toString()}`)
    }, [router, searchParams])

    // Update kind filter in URL params
    const updateKindFilter = useCallback((newKind: string) => {
        const params = new URLSearchParams(searchParams.toString())
        params.delete("cursor")

        if (newKind && newKind !== "ALL") {
            params.set("kind", newKind)
        } else {
            params.delete("kind")
        }

        router.push(`?${params.toString()}`)
    }, [router, searchParams])

    // Update interval range filters
    const updateIntervalFilter = useCallback((min: string, max: string) => {
        const params = new URLSearchParams(searchParams.toString())
        params.delete("cursor")

        if (min) {
            params.set("interval_min", min)
        } else {
            params.delete("interval_min")
        }

        if (max) {
            params.set("interval_max", max)
        } else {
            params.delete("interval_max")
        }

        router.push(`?${params.toString()}`)
    }, [router, searchParams])

    // Update date range filters
    const updateDateFilter = useCallback((after: string, before: string) => {
        const params = new URLSearchParams(searchParams.toString())
        params.delete("cursor")

        if (after) {
            params.set("created_after", after)
        } else {
            params.delete("created_after")
        }

        if (before) {
            params.set("created_before", before)
        } else {
            params.delete("created_before")
        }

        router.push(`?${params.toString()}`)
    }, [router, searchParams])

    // Clear all filters
    const clearAllFilters = useCallback(() => {
        const params = new URLSearchParams()
        router.push(`?${params.toString()}`)
    }, [router])

    if (query.error instanceof Error) {
        toast.error(query.error.message)
    }

    const createWorkflowMutation = useMutation({
        mutationFn: async (workflowData: {
            name: string
            kind: string
            payload: string
            interval: number
            maxConsecutiveJobFailuresAllowed: number
        }) => {
            const response = await fetchWithAuth(WORKFLOWS_ENDPOINT, {
                method: "POST",
                body: JSON.stringify(workflowData)
            })

            if (!response.ok) {
                throw new Error("failed to create workflow")
            }
        },
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: ["workflows"] })
            resetPagination()
            toast.success("workflow created successfully")
        },
        onError: (error) => {
            toast.error(error.message)
        }
    })

    return {
        workflows: query?.data?.workflows || [],
        isLoading: query.isLoading,
        error: query.error,
        createWorkflow: createWorkflowMutation.mutate,
        isCreating: createWorkflowMutation.isPending,
        refetch: query.refetch,
        refetchLoading: query.isRefetching,
        // Search and filter functions
        searchQuery,
        statusFilter,
        kindFilter,
        intervalMin,
        intervalMax,
        createdAfter,
        createdBefore,
        updateSearchQuery,
        updateStatusFilter,
        updateKindFilter,
        updateIntervalFilter,
        updateDateFilter,
        clearAllFilters,
        pagination: {
            nextCursor: query?.data?.cursor,
            hasNextPage: !!query?.data?.cursor,
            hasPreviousPage: !!currentCursor,
            goToNextPage,
            goToPreviousPage,
            resetPagination,
            currentPage: currentCursor ? 'paginated' : 'first'
        }
    }
}