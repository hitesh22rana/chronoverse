"use client"

import { useRouter } from "next/navigation"
import { useMutation, useQuery } from "@tanstack/react-query"
import { toast } from "sonner"

import { fetchWithAuth } from "@/lib/api-client"
import { Workflow } from "@/hooks/use-workflows"
import { useState } from "react"

const API_URL = process.env.NEXT_PUBLIC_API_URL
const WORKFLOW_DETAILS_ENDPOINT = `${API_URL}/workflows`

export type UpdateWorkflowDetails = {
    name: string
    payload: string
    interval: number
    max_consecutive_job_failures_allowed: number
}

export type WorkflowAnalytics = {
    workflow_id: string;
    total_job_execution_duration: number;
    total_jobs: number;
    total_joblogs: number;
}

export function useWorkflowDetails(workflowId: string) {
    const router = useRouter()
    const [workflowNotActive, setWorkflowNotActive] = useState(false)

    const getWorkflowQuery = useQuery<Workflow, Error>({
        queryKey: ["workflow", workflowId],
        queryFn: async () => {
            const response = await fetchWithAuth(`${WORKFLOW_DETAILS_ENDPOINT}/${workflowId}`)

            if (!response.ok) {
                throw new Error("failed to fetch workflow details")
            }

            const data = await response.json() as Workflow
            if (data.build_status === "QUEUED" || data.build_status === "STARTED") {
                setWorkflowNotActive(true)
            }

            return data
        },
        enabled: !!workflowId,
        refetchInterval: workflowNotActive ? 5000 : false, // Refetch every 5 seconds if workflow is not active
    })

    if (getWorkflowQuery.error instanceof Error) {
        toast.error(getWorkflowQuery.error.message)
    }

    const updateWorkflowMutation = useMutation({
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
            getWorkflowQuery.refetch()
        },
        onError: (error) => {
            toast.error(error.message)
        }
    })

    const terminateWorkflowMutation = useMutation({
        mutationFn: async () => {
            const response = await fetchWithAuth(`${WORKFLOW_DETAILS_ENDPOINT}/${workflowId}`, {
                method: "PATCH",
            })

            if (!response.ok) {
                throw new Error("failed to terminate workflow")
            }

            return workflowId
        },
        onSuccess: () => {
            toast.success("workflow terminated successfully")
            getWorkflowQuery.refetch()
            setWorkflowNotActive(false)
        },
        onError: (error) => {
            toast.error(error.message)
        }
    })

    const deleteWorkflowMutation = useMutation({
        mutationFn: async () => {
            const response = await fetchWithAuth(`${WORKFLOW_DETAILS_ENDPOINT}/${workflowId}`, {
                method: "DELETE",
            })

            if (!response.ok) {
                throw new Error("failed to delete workflow")
            }

            return workflowId
        },
        onSuccess: () => {
            toast.success("workflow deleted successfully")
            router.push("/") // Redirect to the dashboard after deletion
        },
        onError: (error) => {
            toast.error(error.message)
        }
    })

    const getWorkflowAnalyticsQuery = useQuery<WorkflowAnalytics, Error>({
        queryKey: ["workflow-analytics", workflowId],
        queryFn: async () => {
            const response = await fetchWithAuth(`${API_URL}/analytics/${workflowId}`)

            if (!response.ok) {
                throw new Error("failed to fetch workflow analytics")
            }

            return response.json() as Promise<WorkflowAnalytics>
        },
        enabled: !!workflowId,
    })

    return {
        workflow: getWorkflowQuery.data as Workflow,
        isLoading: getWorkflowQuery.isLoading,
        error: getWorkflowQuery.error,
        refetch: getWorkflowQuery.refetch,
        updateWorkflow: updateWorkflowMutation.mutate,
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
        analyticsError: getWorkflowAnalyticsQuery.error,
        refetchAnalytics: getWorkflowAnalyticsQuery.refetch,
    }
}