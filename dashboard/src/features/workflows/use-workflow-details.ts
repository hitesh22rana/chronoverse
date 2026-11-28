"use client"

import { useRouter } from "next/navigation"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"

import { createIdempotencyKey, fetchApi, fetchApiJson } from "@/lib/api/client"
import { apiEndpoints } from "@/lib/api/endpoints"
import { queryRefetchIntervals, queryStaleTimes } from "@/lib/api/query-policy"
import { queryKeys } from "@/lib/api/query-keys"
import type { WorkflowAnalytics } from "@/features/analytics/types"
import {
    normalizeWorkflowAnalytics,
    type WorkflowAnalyticsPayload,
} from "@/features/analytics/analytics-data"
import { refreshQueries, refreshWorkflowEdit, useWorkflowScope, workflowDescendants } from "./workflow-freshness"
import type { UpdateWorkflowDetails, Workflow } from "@/features/workflows/types"

type UseWorkflowDetailsOptions = {
    analytics?: boolean
}

export function useWorkflowDetails(
    routeWorkflowId: string,
    { analytics = false }: UseWorkflowDetailsOptions = {},
) {
    const workflowId = routeWorkflowId.toLowerCase()
    const router = useRouter()
    const queryClient = useQueryClient()
    const scope = useWorkflowScope(workflowId)

    const getWorkflowQuery = useQuery<Workflow, Error>({
        queryKey: scope.queryKey(queryKeys.workflow.detail(workflowId)),
        queryFn: ({ signal }) => {
            scope.requireCurrent()
            return fetchApiJson<Workflow>(apiEndpoints.workflows.detail(workflowId), "failed to fetch workflow details", { signal })
        },
        enabled: scope.available,
        refetchInterval: (query) => {
            const buildStatus = query.state.data?.build_status
            return buildStatus === "QUEUED" || buildStatus === "STARTED"
                ? queryRefetchIntervals.activeWorkflowBuild
                : false
        },
        staleTime: queryStaleTimes.workflowDetails,
    })

    if (scope.available && getWorkflowQuery.error instanceof Error) {
        toast.error(getWorkflowQuery.error.message)
    }

    const updateWorkflowMutation = useMutation({
        mutationFn: async (command: { payload: UpdateWorkflowDetails; idempotencyKey: string }) => {
            scope.requireCurrent()
            await fetchApi(apiEndpoints.workflows.detail(workflowId), "failed to update workflow", {
                method: "PUT",
                signal: scope.signal(),
                headers: {
                    "Idempotency-Key": command.idempotencyKey,
                },
                body: JSON.stringify(command.payload),
            })
        },
        onSuccess: async () => {
            if (!scope.isCurrent()) return
            const refresh = refreshWorkflowEdit(queryClient, workflowId, true, scope.isCurrent)
            toast.success("workflow updated successfully")
            await refresh
        },
        onError: (error) => {
            if (scope.isCurrent()) toast.error(error.message)
        }
    })

    const terminateWorkflowMutation = useMutation({
        mutationFn: async () => {
            scope.requireCurrent()
            await fetchApi(apiEndpoints.workflows.detail(workflowId), "failed to terminate workflow", {
                method: "PATCH",
                signal: scope.signal(),
            })

            return workflowId
        },
        onSuccess: async () => {
            if (!scope.isCurrent()) return
            const refresh = refreshWorkflowEdit(queryClient, workflowId, false, scope.isCurrent)
            toast.success("workflow terminated successfully")
            await refresh
        },
        onError: (error) => {
            if (scope.isCurrent()) toast.error(error.message)
        }
    })

    const retireWorkflow = async () => {
        if (!scope.isCurrent()) return
        scope.retire()
        const filters = workflowDescendants(workflowId)
        const cancel = queryClient.cancelQueries(filters)
        const refresh = refreshQueries(queryClient, { predicate: ({ queryKey: [family] }) =>
            family === queryKeys.workflows.all[0] || family === queryKeys.userAnalytics[0] })
        if (window.location.pathname.split("/")[1] === "workflows" &&
            window.location.pathname.split("/")[2]?.toLowerCase() === workflowId) {
            router.replace("/")
        }
        await cancel
        queryClient.removeQueries(filters)
        await refresh
    }

    const deleteWorkflowMutation = useMutation({
        mutationFn: async () => {
            scope.requireCurrent()
            await fetchApi(apiEndpoints.workflows.detail(workflowId), "failed to delete workflow", {
                method: "DELETE",
                signal: scope.signal(),
            })

            return workflowId
        },
        onSuccess: async () => {
            if (!scope.isCurrent()) return
            const cleanup = retireWorkflow()
            toast.success("workflow deleted successfully")
            await cleanup
        },
        onError: async (error) => {
            if (!scope.isCurrent()) return
            const status = "status" in error ? Number(error.status) : undefined
            if (error instanceof TypeError || status === 408 || (status !== undefined && status >= 500)) {
                try {
                    await fetchApi(apiEndpoints.workflows.detail(workflowId), "failed to verify workflow", {
                        signal: AbortSignal.any([scope.signal(), AbortSignal.timeout(10_000)]),
                    })
                } catch (verification) {
                    if (verification instanceof Error && "status" in verification && verification.status === 404) {
                        await retireWorkflow()
                        return
                    }
                }
                if (scope.isCurrent()) toast.error("Could not confirm whether the workflow was deleted")
                return
            }
            toast.error(error.message)
        }
    })

    const getWorkflowAnalyticsQuery = useQuery<WorkflowAnalytics, Error>({
        queryKey: scope.queryKey(queryKeys.workflow.analytics(workflowId)),
        queryFn: async ({ signal }) => {
            scope.requireCurrent()
            return normalizeWorkflowAnalytics(await fetchApiJson<WorkflowAnalyticsPayload>(
                apiEndpoints.analytics.workflow(workflowId), "failed to fetch workflow analytics", { signal },
            ), workflowId)
        },
        enabled: analytics && scope.available,
        staleTime: queryStaleTimes.analytics,
        refetchOnWindowFocus: false,
    })

    return {
        workflow: (scope.available ? getWorkflowQuery.data : undefined) as Workflow,
        isWorkflowAvailable: scope.available,
        isLoading: getWorkflowQuery.isLoading,
        fetchStatus: getWorkflowQuery.fetchStatus,
        error: getWorkflowQuery.error,
        refetch: () => scope.isCurrent() ? getWorkflowQuery.refetch() : undefined,
        updateWorkflow: (payload: UpdateWorkflowDetails, onSuccess?: () => void) => updateWorkflowMutation.mutate({
            payload,
            idempotencyKey: createIdempotencyKey(),
        }, { onSuccess: () => { if (scope.isCurrent()) onSuccess?.() } }),
        isUpdating: updateWorkflowMutation.isPending,
        updateError: updateWorkflowMutation.error,
        terminateWorkflow: terminateWorkflowMutation.mutate,
        isTerminating: terminateWorkflowMutation.isPending,
        terminateError: terminateWorkflowMutation.error,
        deleteWorkflow: deleteWorkflowMutation.mutate,
        isDeleting: deleteWorkflowMutation.isPending,
        deleteError: deleteWorkflowMutation.error,
        workflowAnalytics: scope.available ? getWorkflowAnalyticsQuery.data : undefined,
        isAnalyticsLoading: getWorkflowAnalyticsQuery.isLoading,
        isAnalyticsFetching: getWorkflowAnalyticsQuery.isFetching,
        analyticsError: getWorkflowAnalyticsQuery.error,
        refetchAnalytics: () => scope.isCurrent() ? getWorkflowAnalyticsQuery.refetch() : undefined,
    }
}
