"use client"

import { useQuery } from "@tanstack/react-query"

import { fetchWithAuth } from "@/utils/api-client"

const API_URL = process.env.NEXT_PUBLIC_API_URL
const USER_ENDPOINT = `${API_URL}/users`

type User = {
    email: string
    notification_preference: string
    created_at: string
    updated_at: string
}

export function useUsers() {
    const mutation = useQuery({
        queryKey: ["user"],
        queryFn: async () => {
            const response = await fetchWithAuth(USER_ENDPOINT, {
                method: "GET",
            })

            if (!response.ok) {
                const errorData = await response.json()
                throw new Error(errorData.message || errorData.error || "Failed to fetch user")
            }

            const data = await response.json() as User
            return data
        }
    })

    return {
        user: mutation.data as User,
        isLoading: mutation.isLoading,
        error: mutation.error,
    }
}