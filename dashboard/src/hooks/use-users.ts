"use client"

import { useMutation, useQuery } from "@tanstack/react-query"
import { toast } from "sonner"

import { fetchWithAuth } from "@/lib/api-client"

const USER_ENDPOINT = `${process.env.NEXT_PUBLIC_API_URL}/users`

type User = {
    email: string
    notification_preference: 'ALERTS' | 'ALL' | 'NONE'
    created_at: string
    updated_at: string
}

export type UpdateUserDetails = {
    notification_preference: string
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

    return {
        user: getUserQuery.data as User,
        isLoading: getUserQuery.isLoading,
        error: getUserQuery.error,
        refetch: getUserQuery.refetch,
        updateUser: updateUser.mutate,
        isUpdating: updateUser.isPending,
        updateError: updateUser.error,
    }
}
