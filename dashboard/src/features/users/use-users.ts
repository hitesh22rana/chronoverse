"use client"

import { useMutation, useQuery } from "@tanstack/react-query"
import { toast } from "sonner"

import { fetchApi, fetchApiJson } from "@/lib/api/client"
import { apiEndpoints } from "@/lib/api/endpoints"
import { queryKeys } from "@/lib/api/query-keys"
import type { UpdateUserDetails, User } from "@/features/users/types"

export function useUsers() {
    const getUserQuery = useQuery<User, Error>({
        queryKey: queryKeys.user,
        queryFn: () => fetchApiJson<User>(apiEndpoints.users, "failed to fetch user"),
    })

    const updateUser = useMutation({
        mutationFn: async (updatedUser: UpdateUserDetails) => {
            await fetchApi(apiEndpoints.users, "failed to update user", {
                method: "PUT",
                body: JSON.stringify(updatedUser),
            })
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
