"use client"

import { useInfiniteQuery, useMutation, useQueryClient, type InfiniteData } from "@tanstack/react-query"
import { toast } from "sonner"
import { fetchWithAuth } from "@/utils/api-client"

const API_URL = process.env.NEXT_PUBLIC_API_URL
const NOTIFICATIONS_ENDPOINT = `${API_URL}/notifications`

export type Notifications = {
    notifications: Notification[]
    cursor?: string
}

export type Notification = {
    id: string
    kind: string
    payload: {
        title: string
        message: string
        entity_id: string
        entity_type: string
        action_url: string
    }
    read_at?: string
    created_at: string
    updated_at: string
}

export function useNotifications() {
    const queryClient = useQueryClient()

    const query = useInfiniteQuery<Notifications, Error>({
        queryKey: ["notifications"],
        queryFn: async ({ pageParam }) => {
            const url = pageParam
                ? `${NOTIFICATIONS_ENDPOINT}?cursor=${pageParam}`
                : `${NOTIFICATIONS_ENDPOINT}`
            const response = await fetchWithAuth(url)

            if (!response.ok) {
                throw new Error("failed to fetch notifications")
            }

            return response.json() as Promise<Notifications>
        },
        initialPageParam: null,
        getNextPageParam: (lastPage) => lastPage?.cursor || null,
        refetchInterval: 10000, // 10 seconds
    })

    if (query.error instanceof Error) {
        toast.error(query.error.message)
    }

    const allPages = query.data?.pages || []
    const notifications = allPages.length > 0 ? allPages.flatMap((page) => page?.notifications || []) : []

    const markAsReadMutation = useMutation({
        mutationFn: async (ids: string[]) => {
            // To make sure we don't send too many ids in one request, we batch the ids into smaller arrays
            // This is a simple batching function that splits the array into small batches
            const batchSize = 100
            const batchs: string[][] = []
            for (let i = 0; i < ids.length; i += batchSize) {
                batchs.push(ids.slice(i, i + batchSize))
            }

            // Send a request for each batch
            for (const batch of batchs) {
                const _ids = batch
                const response = await fetchWithAuth(`${NOTIFICATIONS_ENDPOINT}/read`, {
                    method: "PUT",
                    headers: {
                        "Content-Type": "application/json",
                    },
                    body: JSON.stringify({ ids: _ids }),
                })

                if (!response.ok) {
                    throw new Error("failed to mark notifications as read")
                }
            }

            return ids
        },
        onSuccess: (ids) => {
            queryClient.setQueryData(["notifications"], (oldData: InfiniteData<Notifications> | undefined) => {
                if (!oldData) return oldData

                // Remove the notifications from the old data
                const updatedPages = oldData.pages.map((page) => {
                    const updatedNotifications = page.notifications.filter((notification) => !ids.includes(notification.id))
                    return { ...page, notifications: updatedNotifications }
                })

                return {
                    ...oldData,
                    pages: updatedPages,
                }
            })
        },
        onError: (error) => {
            toast.error(error.message)
        },
    })

    return {
        notifications,
        isLoading: query.isLoading,
        error: query.error,
        markAsRead: markAsReadMutation.mutate,
        refetch: query.refetch,
        fetchNextPage: query.fetchNextPage,
        isFetchingNextPage: query.isFetchingNextPage,
        hasNextPage: query.hasNextPage,
    }
}
