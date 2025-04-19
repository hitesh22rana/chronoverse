"use client"

import { useMutation, useQuery } from "@tanstack/react-query"
import { toast } from "sonner"

import { fetchWithAuth } from "@/utils/api-client"
import { Workflow } from "@/hooks/use-workflows"

const API_URL = process.env.NEXT_PUBLIC_API_URL
const WORKFLOW_DETAILS_ENDPOINT = `${API_URL}/workflows`

export type UpdateWorkflowDetails = {
    name: string
    payload: string
    interval: number
    max_consecutive_job_failures_allowed: number
}

export function useWorkflowDetails(workflowId: string) {
    const query = useQuery({
        queryKey: ["workflow", workflowId],
        queryFn: async () => {
            const response = await fetchWithAuth(`${WORKFLOW_DETAILS_ENDPOINT}/${workflowId}`)

            if (!response.ok) {
                throw new Error("failed to fetch workflow details")
            }

            const data = await response.json()
            return data as Workflow
        },
    })

    if (query.error instanceof Error) {
        toast.error(query.error.message)
    }

    const updateWorkflow = useMutation({
        mutationFn: async (updatedWorkflowDetails: UpdateWorkflowDetails) => {
            const response = await fetchWithAuth(`${WORKFLOW_DETAILS_ENDPOINT}/${workflowId}`, {
                method: "PUT",
                body: JSON.stringify(updatedWorkflowDetails),
            })

            if (!response.ok) {
                throw new Error("failed to update workflow")
            }
        },
        onSuccess: () => {
            toast.success("workflow updated successfully")
            query.refetch()
        },
        onError: (error) => {
            toast.error(error.message)
        }
    })

    return {
        workflow: query.data as Workflow,
        isLoading: query.isLoading,
        error: query.error,
        updateWorkflow: updateWorkflow.mutate,
        isUpdating: updateWorkflow.isPending,
        updateError: updateWorkflow.error,
        refetch: query.refetch,
    }
}