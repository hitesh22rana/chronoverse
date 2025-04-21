"use client"

import { useCallback, useMemo } from "react"
import { usePathname, useRouter, useSearchParams } from "next/navigation"
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"

import { fetchWithAuth } from "@/utils/api-client"

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
    const router = useRouter()
    const path = usePathname()
    const searchParams = useSearchParams()
    const queryClient = useQueryClient()

    const isNotRootPath = path !== "/"

    let currentCursor = ""
    let searchQuery = ""
    let statusFilter = "ALL"

    if (isNotRootPath) {
        currentCursor = ""
        searchQuery = ""
        statusFilter = "ALL"
    } else {
        currentCursor = searchParams.get("cursor") || ""
        searchQuery = searchParams.get("search") || ""
        statusFilter = searchParams.get("status") || "ALL"
    }

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

            return response.json() as Promise<WorkflowsResponse>
        },
        refetchInterval: 10000, // Refetch every 10 seconds
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

    // Apply filters inside the hook
    const filteredWorkflows = useMemo(() => {
        return query?.data?.workflows.filter(workflow => {
            // Search filter
            const matchesSearch = !searchQuery ||
                workflow.name.toLowerCase().includes(searchQuery.toLowerCase())

            // Status filter
            const normalizedStatusFilter = statusFilter === "ALL" ? null : statusFilter

            if (!!workflow?.terminated_at) {
                return matchesSearch && (normalizedStatusFilter === "TERMINATED" || !normalizedStatusFilter)
            }

            const matchesStatus = !normalizedStatusFilter ||
                (normalizedStatusFilter === "TERMINATED"
                    ? !!workflow.terminated_at
                    : workflow.build_status === normalizedStatusFilter)

            return matchesSearch && matchesStatus
        })
    }, [query?.data?.workflows, searchQuery, statusFilter])

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
        workflows: filteredWorkflows || [],
        isLoading: query.isLoading,
        error: query.error,
        createWorkflow: createWorkflowMutation.mutate,
        isCreating: createWorkflowMutation.isPending,
        refetch: query.refetch,
        refetchLoading: query.isRefetching,
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