"use client"

import { useQuery } from "@tanstack/react-query"

import { fetchWithAuth } from "@/lib/api-client"

const API_URL = process.env.NEXT_PUBLIC_API_URL

export type WorkflowKindAnalytics = {
    kind: string
    total_workflows: number
    total_jobs: number
    total_joblogs: number
    total_job_execution_duration: number
}

export type WorkflowAnalyticsSummary = {
    workflow_id: string
    workflow_name: string
    kind: string
    total_jobs: number
    total_joblogs: number
    total_job_execution_duration: number
}

export type UserAnalytics = {
    total_workflows: number
    total_jobs: number
    total_joblogs: number
    total_job_execution_duration: number
    workflow_kinds?: WorkflowKindAnalytics[]
    top_workflows?: WorkflowAnalyticsSummary[]
}

export function useUserAnalytics(enabled: boolean) {
    return useQuery<UserAnalytics, Error>({
        queryKey: ["user-analytics"],
        queryFn: async () => {
            const response = await fetchWithAuth(`${API_URL}/analytics`)

            if (!response.ok) {
                throw new Error("failed to fetch analytics")
            }

            return response.json() as Promise<UserAnalytics>
        },
        enabled,
        staleTime: 5 * 60 * 1000,
        refetchOnWindowFocus: false,
    })
}
