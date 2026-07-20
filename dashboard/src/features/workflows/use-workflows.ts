"use client"

import { usePathname, useRouter, useSearchParams } from "next/navigation"
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"

import { createIdempotencyKey, fetchApi, fetchApiJson } from "@/lib/api/client"
import { apiEndpoints, withQuery } from "@/lib/api/endpoints"
import { queryKeys } from "@/lib/api/query-keys"
import type { CreateWorkflowPayload, WorkflowsResponse } from "@/features/workflows/types"

import { normalizeIntervalFilter } from "@/features/workflows/interval-filter"

type UseWorkflowsOptions = {
    poll?: boolean
}

export function useWorkflows({ poll = false }: UseWorkflowsOptions = {}) {
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
        intervalMin = normalizeIntervalFilter(searchParams.get("interval_min"))
        intervalMax = normalizeIntervalFilter(searchParams.get("interval_max"))
    }

    // Build query parameters for the get workflows request
    const getWorkflowQueryParams = (() => {
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

        const normalizedIntervalMin = normalizeIntervalFilter(intervalMin)
        if (normalizedIntervalMin) {
            params.set("interval_min", normalizedIntervalMin)
        }

        const normalizedIntervalMax = normalizeIntervalFilter(intervalMax)
        if (normalizedIntervalMax) {
            params.set("interval_max", normalizedIntervalMax)
        }

        return params.toString()
    })()

    const getWorkflowQuery = useQuery<WorkflowsResponse, Error>({
        queryKey: queryKeys.workflows.list(
            currentCursor,
            searchQuery,
            statusFilter,
            kindFilter,
            intervalMin,
            intervalMax,
        ),
        queryFn: () => fetchApiJson<WorkflowsResponse>(
            withQuery(apiEndpoints.workflows.list, getWorkflowQueryParams),
            "failed to fetch workflows",
        ),
        refetchInterval: poll
            ? (query) => {
                const workflows = query.state.data?.workflows ?? []
                const hasBuildInProgress = workflows.some((workflow) =>
                    workflow.build_status === "QUEUED" || workflow.build_status === "STARTED"
                )

                return hasBuildInProgress ? 5000 : 60000
            }
            : false,
        refetchIntervalInBackground: false,
    })

    const goToNextPage = () => {
        const nextCursor = getWorkflowQuery?.data?.cursor
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

    // Update search query in URL params
    const updateSearchQuery = (newSearchQuery: string) => {
        const params = new URLSearchParams(searchParams.toString())
        params.delete("cursor") // Reset pagination when searching

        if (newSearchQuery) {
            params.set("query", newSearchQuery)
        } else {
            params.delete("query")
        }

        router.push(`?${params.toString()}`)
    }

    // Apply all filters and search query
    const applyAllFilters = (filters: unknown) => {
        const params = new URLSearchParams(searchParams.toString())
        params.delete("cursor") // Reset pagination when applying filters

        const {
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

        const normalizedIntervalMin = normalizeIntervalFilter(intervalMin)
        if (normalizedIntervalMin) {
            params.set("interval_min", normalizedIntervalMin)
        } else {
            params.delete("interval_min")
        }

        const normalizedIntervalMax = normalizeIntervalFilter(intervalMax)
        if (normalizedIntervalMax) {
            params.set("interval_max", normalizedIntervalMax)
        } else {
            params.delete("interval_max")
        }

        router.push(`?${params.toString()}`)
    }

    // Clear all filters
    const clearAllFilters = () => {
        // Get the search query if it exists
        const oldParams = new URLSearchParams(searchParams.toString())
        const query = oldParams.get("query")

        const params = new URLSearchParams()
        if (query) {
            params.set("query", query)
        }

        router.push(`?${params.toString()}`)
    }

    if (getWorkflowQuery.error instanceof Error) {
        toast.error(getWorkflowQuery.error.message)
    }

    const createWorkflowMutation = useMutation({
        mutationFn: async (workflowPayload: CreateWorkflowPayload) => {
            await fetchApi(apiEndpoints.workflows.list, "failed to create workflow", {
                method: "POST",
                headers: {
                    "Idempotency-Key": createIdempotencyKey(),
                },
                body: JSON.stringify(workflowPayload)
            })
        },
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: queryKeys.workflows.all })
            resetPagination()
            toast.success("workflow created successfully")
        },
        onError: (error) => {
            toast.error(error.message)
        }
    })

    return {
        workflows: getWorkflowQuery?.data?.workflows || [],
        isLoading: getWorkflowQuery.isLoading,
        error: getWorkflowQuery.error,
        createWorkflow: createWorkflowMutation.mutate,
        isCreating: createWorkflowMutation.isPending,
        refetch: getWorkflowQuery.refetch,
        refetchLoading: getWorkflowQuery.isRefetching,
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
            nextCursor: getWorkflowQuery?.data?.cursor,
            hasNextPage: !!getWorkflowQuery?.data?.cursor,
            hasPreviousPage: !!currentCursor,
            goToNextPage,
            goToPreviousPage,
            resetPagination,
            currentPage: currentCursor ? 'paginated' : 'first'
        }
    }
}
