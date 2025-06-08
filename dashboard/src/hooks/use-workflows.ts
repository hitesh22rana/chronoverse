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

    if (isNotRootPath) {
        currentCursor = ""
        searchQuery = ""
        statusFilter = ""
        kindFilter = ""
        intervalMin = ""
        intervalMax = ""
    } else {
        currentCursor = searchParams.get("cursor") || ""
        searchQuery = searchParams.get("query") || ""
        statusFilter = searchParams.get("status") || ""
        kindFilter = searchParams.get("kind") || ""
        intervalMin = searchParams.get("interval_min") || ""
        intervalMax = searchParams.get("interval_max") || ""
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

        return params.toString()
    }, [currentCursor, searchQuery, statusFilter, kindFilter, intervalMin, intervalMax])

    const query = useQuery({
        queryKey: ["workflows", currentCursor, searchQuery, statusFilter, kindFilter, intervalMin, intervalMax],
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

    // Apply all filters and search query
    const applyAllFilters = useCallback((filters: unknown) => {
        const params = new URLSearchParams(searchParams.toString())
        params.delete("cursor") // Reset pagination when applying filters

        const
            {
                status,
                kind,
                intervalMin,
                intervalMax
            } = filters as {
                status?: string,
                kind?: string,
                intervalMin?: string,
                intervalMax?: string,
            }

        if (status && status !== "ALL") {
            params.set("status", status)
        } else {
            params.delete("status")
        }

        if (kind && kind !== "ALL") {
            params.set("kind", kind)
        } else {
            params.delete("kind")
        }

        if (intervalMin) {
            params.set("interval_min", intervalMin)
        } else {
            params.delete("interval_min")
        }

        if (intervalMax) {
            params.set("interval_max", intervalMax)
        } else {
            params.delete("interval_max")
        }

        router.push(`?${params.toString()}`)
    }, [router, searchParams])

    // Clear all filters
    const clearAllFilters = useCallback(() => {
        // Get the search query if it exists
        const oldParams = new URLSearchParams(searchParams.toString())
        const query = oldParams.get("query")

        const params = new URLSearchParams()
        if (query) {
            params.set("query", query)
        }

        router.push(`?${params.toString()}`)
    }, [router, searchParams])

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
        updateSearchQuery,
        applyAllFilters,
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