"use client"

import { usePathname, useRouter, useSearchParams } from "next/navigation"
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"

import { createIdempotencyKey, fetchApi, fetchApiJson } from "@/lib/api/client"
import { apiEndpoints, withQuery } from "@/lib/api/endpoints"
import { queryRefetchIntervals, queryStaleTimes } from "@/lib/api/query-policy"
import { queryKeys } from "@/lib/api/query-keys"
import type { CreateWorkflowPayload, WorkflowsResponse } from "@/features/workflows/types"

import { normalizeIntervalFilter } from "@/features/workflows/interval-filter"

type UseWorkflowsOptions = {
    poll?: boolean
}

type SearchParams = Pick<URLSearchParams, "get" | "toString">

type WorkflowFilters = {
    status?: string
    kind?: string
    intervalMin?: string
    intervalMax?: string
}

function readWorkflowFilters(path: string, searchParams: SearchParams) {
    const params = path === "/" ? searchParams : new URLSearchParams()
    return {
        currentCursor: params.get("cursor") || "",
        searchQuery: params.get("query") || "",
        statusFilter: params.get("status") || "",
        kindFilter: params.get("kind") || "",
        intervalMin: normalizeIntervalFilter(params.get("interval_min")),
        intervalMax: normalizeIntervalFilter(params.get("interval_max")),
    }
}

function workflowQueryParams({
    currentCursor,
    searchQuery,
    statusFilter,
    kindFilter,
    intervalMin,
    intervalMax,
}: ReturnType<typeof readWorkflowFilters>) {
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
}

function applyWorkflowFilters(params: URLSearchParams, filters: WorkflowFilters) {
    const { status, kind, intervalMin, intervalMax } = filters

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
}

export function useWorkflows({ poll = false }: UseWorkflowsOptions = {}) {
    const queryClient = useQueryClient()
    const router = useRouter()
    const path = usePathname()
    const searchParams = useSearchParams()

    const filters = readWorkflowFilters(path, searchParams)
    const { currentCursor, searchQuery, statusFilter, kindFilter, intervalMin, intervalMax } = filters
    const getWorkflowQueryParams = workflowQueryParams(filters)

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

                return hasBuildInProgress
                    ? queryRefetchIntervals.activeWorkflowBuild
                    : queryRefetchIntervals.idleWorkflowList
            }
            : false,
        refetchIntervalInBackground: false,
        staleTime: queryStaleTimes.workflowList,
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

    const applyAllFilters = (filters: unknown) => {
        const params = new URLSearchParams(searchParams.toString())
        params.delete("cursor") // Reset pagination when applying filters

        applyWorkflowFilters(params, filters as WorkflowFilters)

        router.push(`?${params.toString()}`)
    }

    const clearAllFilters = () => {
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
        mutationFn: async (command: { payload: CreateWorkflowPayload; idempotencyKey: string }) => {
            await fetchApi(apiEndpoints.workflows.list, "failed to create workflow", {
                method: "POST",
                headers: {
                    "Idempotency-Key": command.idempotencyKey,
                },
                body: JSON.stringify(command.payload)
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
        createWorkflow: (payload: CreateWorkflowPayload, onSuccess?: () => void) => createWorkflowMutation.mutate({
            payload,
            idempotencyKey: createIdempotencyKey(),
        }, { onSuccess }),
        isCreating: createWorkflowMutation.isPending,
        refetch: getWorkflowQuery.refetch,
        refetchLoading: getWorkflowQuery.isRefetching,
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
