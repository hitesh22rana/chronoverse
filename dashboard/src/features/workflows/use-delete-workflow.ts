"use client"

import { useMutation, useQueryClient } from "@tanstack/react-query"
import { useRouter } from "next/navigation"
import { toast } from "sonner"

import { fetchApi } from "@/lib/api/client"
import { apiEndpoints } from "@/lib/api/endpoints"
import { queryKeys } from "@/lib/api/query-keys"
import { workflowIdFromPath } from "@/lib/workflow-route"

import { refreshQueries, workflowDescendants, type useWorkflowScope } from "./workflow-freshness"

function statusOf(error: unknown) {
    return error instanceof Error && "status" in error ? Number(error.status) : undefined
}

async function checkWorkflowAfterDelete(url: string, signal: AbortSignal) {
    try {
        await fetchApi(url, "failed to verify workflow", {
            signal: AbortSignal.any([signal, AbortSignal.timeout(10_000)]),
        })
        return "available"
    } catch (error) {
        const status = statusOf(error)
        if (status === 404) return "missing"
        if (status === 401 || status === 403) return "inaccessible"
        return "unknown"
    }
}

export function useDeleteWorkflow(workflowId: string, scope: ReturnType<typeof useWorkflowScope>) {
    const router = useRouter()
    const queryClient = useQueryClient()
    const url = apiEndpoints.workflows.detail(workflowId)

    function discardWorkflowReads(deleted: boolean) {
        scope.retire(deleted)
        const filters = workflowDescendants(workflowId)
        const cancel = queryClient.cancelQueries(filters)
        void cancel.then(() => queryClient.removeQueries(filters))
        if (workflowIdFromPath(window.location.pathname) === workflowId) router.replace("/")
        void refreshQueries(queryClient, { predicate: ({ queryKey: [family] }) =>
            family === queryKeys.workflows.all[0] || family === queryKeys.userAnalytics[0] })
    }

    return useMutation({
        mutationFn: async () => {
            scope.requireCurrent()
            await fetchApi(url, "failed to delete workflow", { method: "DELETE", signal: scope.signal() })
        },
        onSuccess: () => {
            if (!scope.isCurrent()) return
            discardWorkflowReads(true)
            toast.success("workflow deleted successfully")
        },
        onError: async (error) => {
            if (!scope.isCurrent()) return
            const status = statusOf(error)
            if (status === 404) {
                discardWorkflowReads(true)
                return
            }
            // An auth or CSRF rejection on DELETE does not prove the resource is gone.
            if (!shouldVerifyDeletion(error, status)) {
                toast.error(error.message)
                return
            }
            const outcome = await checkWorkflowAfterDelete(url, scope.signal())
            if (!scope.isCurrent()) return
            if (outcome === "missing" || outcome === "inaccessible") {
                discardWorkflowReads(outcome === "missing")
                return
            }
            toast.error(outcome === "available"
                ? "Delete request failed. The workflow is still available"
                : "Could not confirm whether the workflow was deleted")
        },
    })
}

function shouldVerifyDeletion(error: Error, status: number | undefined) {
    return error instanceof TypeError || status === 401 || status === 403 ||
        status === 408 || (status !== undefined && status >= 500)
}
