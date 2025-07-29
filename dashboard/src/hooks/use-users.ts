"use client"

import { useMutation, useQuery } from "@tanstack/react-query"
import { toast } from "sonner"

import { fetchWithAuth } from "@/lib/api-client"

const API_URL = process.env.NEXT_PUBLIC_API_URL
const USER_ENDPOINT = `${API_URL}/users`

type User = {
    email: string
    notification_preference: string
    created_at: string
    updated_at: string
}

export type UpdateUserDetails = {
    password: string
    notification_preference: string
}

export type UserAnalytics = {
    totalWorkflows: number;
    totalJobs: number;
    totalJoblogs: number;
}

export function useUsers() {
    const getUserQuery = useQuery<User, Error>({
        queryKey: ["user"],
        queryFn: async () => {
            const response = await fetchWithAuth(USER_ENDPOINT, {
                method: "GET",
            })

            if (!response.ok) {
                throw new Error("failed to fetch user")
            }

            return response.json() as Promise<User>
        }
    })

    const updateUser = useMutation({
        mutationFn: async (updatedUser: UpdateUserDetails) => {
            const response = await fetchWithAuth(USER_ENDPOINT, {
                method: "PUT",
                body: JSON.stringify(updatedUser),
            })

            if (!response.ok) {
                throw new Error("failed to update user")
            }
        },
        onSuccess: () => {
            toast.success("user updated successfully")
            getUserQuery.refetch()
        },
        onError: (error) => {
            toast.error(error.message)
        }
    })

    if (getUserQuery.error instanceof Error) {
        toast.error(getUserQuery.error.message)
    }

    const getUserAnalyticsQuery = useQuery<UserAnalytics, Error>({
        queryKey: ["user-analytics"],
        queryFn: async () => {
            const response = await fetchWithAuth(`${API_URL}/analytics`)

            if (!response.ok) {
                throw new Error("failed to fetch user analytics")
            }

            return response.json() as Promise<UserAnalytics>
        }
    })

    if (getUserAnalyticsQuery.error instanceof Error) {
        toast.error(getUserAnalyticsQuery.error.message)
    }

    return {
        user: getUserQuery.data as User,
        isLoading: getUserQuery.isLoading,
        error: getUserQuery.error,
        refetch: getUserQuery.refetch,
        updateUser: updateUser.mutate,
        isUpdating: updateUser.isPending,
        updateError: updateUser.error,
        userAnalytics: getUserAnalyticsQuery.data,
        isAnalyticsLoading: getUserAnalyticsQuery.isLoading,
        analyticsError: getUserAnalyticsQuery.error,
        refetchAnalytics: getUserAnalyticsQuery.refetch,
        isAnalyticsRefetching: getUserAnalyticsQuery.isRefetching,
    }
}