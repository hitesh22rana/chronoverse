"use client"

import { useState } from "react"
import { useQuery } from "@tanstack/react-query"
import { toast } from "sonner"

import { fetchWithAuth } from "@/lib/api-client"

const API_URL = process.env.NEXT_PUBLIC_API_URL

export type Job = {
    id: string
    workflow_id: string
    status: string
    scheduled_at: string
    started_at?: string
    completed_at?: string
    created_at: string
    updated_at: string
}

export function useJobDetails(workflowId: string, jobId: string) {
    const [disableRefetch, setDisableRefetch] = useState(false)

    const query = useQuery({
        queryKey: ["job-details", workflowId, jobId],
        queryFn: async () => {
            const response = await fetchWithAuth(`${API_URL}/workflows/${workflowId}/jobs/${jobId}`)

            if (!response.ok) {
                throw new Error("failed to fetch job details")
            }

            const data = await (await response.json() as Promise<Job>)
            // Check if the job is completed
            if (data.completed_at) {
                setDisableRefetch(true)
            }
            return data
        },
        refetchInterval: disableRefetch ? false : 5000, // Refetch every 5 seconds if not completed
    })

    if (query.error instanceof Error) {
        toast.error(query.error.message)
    }

    return {
        job: query.data as Job,
        isLoading: query.isLoading,
        error: query.error,
        refetch: query.refetch,
    }
}