"use client"

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
import { refreshWorkflowEdit, useWorkflowScope } from "./workflow-freshness"
import { useDeleteWorkflow } from "./use-delete-workflow"
import type { UpdateWorkflowDetails, Workflow } from "@/features/workflows/types"

type UseWorkflowDetailsOptions = {
    analytics?: boolean
}

export function useWorkflowDetails(
    routeWorkflowId: string,
    { analytics = false }: UseWorkflowDetailsOptions = {},
) {
    const workflowId = routeWorkflowId.toLowerCase()
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
        onSuccess: () => {
            if (!scope.isCurrent()) return
            void refreshWorkflowEdit(queryClient, workflowId, true, scope.isCurrent)
            toast.success("workflow updated successfully")
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
        onSuccess: () => {
            if (!scope.isCurrent()) return
            void refreshWorkflowEdit(queryClient, workflowId, false, scope.isCurrent)
            toast.success("workflow terminated successfully")
        },
        onError: (error) => {
            if (scope.isCurrent()) toast.error(error.message)
        }
    })

    const deleteWorkflowMutation = useDeleteWorkflow(workflowId, scope)

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
