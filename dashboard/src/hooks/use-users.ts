"use client"

import { useQuery } from "@tanstack/react-query"
import { toast } from "sonner"

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
    const query = useQuery({
        queryKey: ["user"],
        queryFn: async () => {
            const response = await fetchWithAuth(USER_ENDPOINT, {
                method: "GET",
            })

            if (!response.ok) {
                throw new Error("failed to fetch user")
            }

            const data = await response.json() as User
            return data
        }
    })

    if (query.error instanceof Error) {
        toast.error(query.error.message)
    }

    return {
        user: query.data as User,
        isLoading: query.isLoading,
        error: query.error,
    }
}