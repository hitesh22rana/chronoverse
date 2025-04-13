"use client"

import { useCallback } from "react"
import { useRouter, useSearchParams } from "next/navigation"
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"

import { fetchWithAuth } from "@/utils/api-client"

const API_URL = process.env.NEXT_PUBLIC_API_URL
const WORKFLOWS_ENDPOINT = `${API_URL}/workflows`

export type WorkflowsResponse = {
    workflows: Workflow[]
    cursor: string | null
}

export type Workflow = {
    id: string
    name: string
    kind: string
    payload: string
    workflowBuildStatus: string
    interval: number
    consecutiveJobFailuresCount: number
    maxConsecutiveJobFailuresAllowed: number
    createdAt: string
    updatedAt: string
    terminatedAt: string | null
}

export function useWorkflows() {
    const router = useRouter()
    const searchParams = useSearchParams()
    const queryClient = useQueryClient()

    const currentCursor = searchParams.get("cursor")

    const query = useQuery({
        queryKey: ["workflows", currentCursor],
        queryFn: async () => {
            const url = currentCursor
                ? `${WORKFLOWS_ENDPOINT}?cursor=${currentCursor}`
                : WORKFLOWS_ENDPOINT

            const response = await fetchWithAuth(url)

            if (!response.ok) {
                throw new Error("failed to fetch workflows")
            }

            const data = await response.json()
            return data as WorkflowsResponse
        }
    })

    const goToNextPage = useCallback(() => {
        const nextCursor = query.data?.cursor
        if (!nextCursor) return false

        const params = new URLSearchParams(searchParams.toString())
        params.set("cursor", nextCursor)
        router.push(`?${params.toString()}`)
        return true
    }, [query.data?.cursor, router, searchParams])

    const goToPreviousPage = useCallback(() => {
        router.back()
        return true
    }, [router])

    const resetPagination = useCallback(() => {
        const params = new URLSearchParams(searchParams.toString())
        params.delete("cursor")
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

            return response.json()
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

    const terminateWorkflowMutation = useMutation({
        mutationFn: async (id: string) => {
            const response = await fetchWithAuth(`${WORKFLOWS_ENDPOINT}/${id}/terminate`, {
                method: "POST"
            })

            if (!response.ok) {
                throw new Error("Failed to terminate workflow")
            }

            return id
        },
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: ["workflows"] })
            toast.success("workflow terminated successfully")
        },
        onError: (error) => {
            toast.error(error.message)
        }
    })

    return {
        workflows: query.data?.workflows || [],
        isLoading: query.isLoading,
        error: query.error,
        createWorkflow: createWorkflowMutation.mutate,
        isCreating: createWorkflowMutation.isPending,
        terminateWorkflow: terminateWorkflowMutation.mutate,
        isTerminating: terminateWorkflowMutation.isPending,
        refetch: query.refetch,
        pagination: {
            nextCursor: query.data?.cursor,
            hasNextPage: !!query.data?.cursor,
            hasPreviousPage: !!currentCursor,
            goToNextPage,
            goToPreviousPage,
            resetPagination,
            currentPage: currentCursor ? 'paginated' : 'first'
        }
    }
}