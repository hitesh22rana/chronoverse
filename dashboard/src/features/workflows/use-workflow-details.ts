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
import type { UpdateWorkflowDetails, Workflow } from "@/features/workflows/types"

type UseWorkflowDetailsOptions = {
    analytics?: boolean
}

export function useWorkflowDetails(
    workflowId: string,
    { analytics = false }: UseWorkflowDetailsOptions = {},
) {
    const router = useRouter()
    const queryClient = useQueryClient()

    const getWorkflowQuery = useQuery<Workflow, Error>({
        queryKey: queryKeys.workflow.detail(workflowId),
        queryFn: () => fetchApiJson<Workflow>(
            apiEndpoints.workflows.detail(workflowId),
            "failed to fetch workflow details",
        ),
        enabled: !!workflowId,
        refetchInterval: (query) => {
            const buildStatus = query.state.data?.build_status
            return buildStatus === "QUEUED" || buildStatus === "STARTED"
                ? queryRefetchIntervals.activeWorkflowBuild
                : false
        },
        staleTime: queryStaleTimes.workflowDetails,
    })

    if (getWorkflowQuery.error instanceof Error) {
        toast.error(getWorkflowQuery.error.message)
    }

    const updateWorkflowMutation = useMutation({
        mutationFn: async (command: { payload: UpdateWorkflowDetails; idempotencyKey: string }) => {
            await fetchApi(apiEndpoints.workflows.detail(workflowId), "failed to update workflow", {
                method: "PUT",
                headers: {
                    "Idempotency-Key": command.idempotencyKey,
                },
                body: JSON.stringify(command.payload),
            })
        },
        onSuccess: () => {
            toast.success("workflow updated successfully")
            queryClient.invalidateQueries({ queryKey: queryKeys.workflows.all })
            getWorkflowQuery.refetch()
        },
        onError: (error) => {
            toast.error(error.message)
        }
    })

    const terminateWorkflowMutation = useMutation({
        mutationFn: async () => {
            await fetchApi(apiEndpoints.workflows.detail(workflowId), "failed to terminate workflow", {
                method: "PATCH",
            })

            return workflowId
        },
        onSuccess: () => {
            toast.success("workflow terminated successfully")
            queryClient.invalidateQueries({ queryKey: queryKeys.workflows.all })
            getWorkflowQuery.refetch()
        },
        onError: (error) => {
            toast.error(error.message)
        }
    })

    const deleteWorkflowMutation = useMutation({
        mutationFn: async () => {
            await fetchApi(apiEndpoints.workflows.detail(workflowId), "failed to delete workflow", {
                method: "DELETE",
            })

            return workflowId
        },
        onSuccess: () => {
            toast.success("workflow deleted successfully")
            queryClient.invalidateQueries({ queryKey: queryKeys.workflows.all })
            queryClient.removeQueries({ queryKey: queryKeys.workflow.detail(workflowId) })
            router.push("/") // Redirect to the dashboard after deletion
        },
        onError: (error) => {
            toast.error(error.message)
        }
    })

    const getWorkflowAnalyticsQuery = useQuery<WorkflowAnalytics, Error>({
        queryKey: queryKeys.workflow.analytics(workflowId),
        queryFn: async () => normalizeWorkflowAnalytics(
            await fetchApiJson<WorkflowAnalyticsPayload>(
                apiEndpoints.analytics.workflow(workflowId),
                "failed to fetch workflow analytics",
            ),
            workflowId,
        ),
        enabled: analytics && !!workflowId,
        staleTime: queryStaleTimes.analytics,
        refetchOnWindowFocus: false,
    })

    return {
        workflow: getWorkflowQuery.data as Workflow,
        isLoading: getWorkflowQuery.isLoading,
        error: getWorkflowQuery.error,
        refetch: getWorkflowQuery.refetch,
        updateWorkflow: (payload: UpdateWorkflowDetails) => updateWorkflowMutation.mutate({
            payload,
            idempotencyKey: createIdempotencyKey(),
        }),
        isUpdating: updateWorkflowMutation.isPending,
        updateError: updateWorkflowMutation.error,
        terminateWorkflow: terminateWorkflowMutation.mutate,
        isTerminating: terminateWorkflowMutation.isPending,
        terminateError: terminateWorkflowMutation.error,
        deleteWorkflow: deleteWorkflowMutation.mutate,
        isDeleting: deleteWorkflowMutation.isPending,
        deleteError: deleteWorkflowMutation.error,
        workflowAnalytics: getWorkflowAnalyticsQuery.data,
        isAnalyticsLoading: getWorkflowAnalyticsQuery.isLoading,
        isAnalyticsFetching: getWorkflowAnalyticsQuery.isFetching,
        analyticsError: getWorkflowAnalyticsQuery.error,
        refetchAnalytics: getWorkflowAnalyticsQuery.refetch,
    }
}
