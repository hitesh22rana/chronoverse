"use client"

import { useInfiniteQuery, useMutation, useQueryClient, InfiniteData } from "@tanstack/react-query"
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

            // Parse JSON response
            const data = await response.json()
            return data as Notifications
        },
        initialPageParam: null,
        getNextPageParam: (lastPage: Notifications) => lastPage?.cursor || null,
        getPreviousPageParam: (firstPage: Notifications) => firstPage?.cursor || null,
        refetchInterval: 10000, // Refetch every 10 seconds
    })

    if (query.error instanceof Error) {
        toast.error(query.error.message)
    }

    const allPages = query.data?.pages || []

    const notifications = allPages.length > 0
        ? allPages.flatMap((page) => page?.notifications || [])
        : []

    // Get cursor from last page with null fallback
    const cursor = allPages.length > 0
        ? (allPages[allPages.length - 1]?.cursor || null)
        : null

    const markAsReadMutation = useMutation({
        mutationFn: async (id: string) => {
            const response = await fetchWithAuth(`${NOTIFICATIONS_ENDPOINT}/${id}/read`, {
                method: "POST"
            })

            if (!response.ok) {
                throw new Error("failed to mark notification as read")
            }

            return id
        },
        onSuccess: (id) => {
            queryClient.setQueryData(
                ["notifications"],
                (oldData: InfiniteData<Notifications> | undefined) => {
                    if (!oldData) return oldData

                    // Update notifications in all pages
                    const updatedPages = oldData.pages.map((page: Notifications) => ({
                        ...page,
                        notifications: page?.notifications?.map((notification) =>
                            notification.id === id
                                ? { ...notification, read_at: new Date().toISOString() }
                                : notification
                        )

                    }))

                    return {
                        ...oldData,
                        pages: updatedPages
                    }
                }
            )
        },
        onError: (error) => {
            toast.error(error.message)
        }
    })

    return {
        notifications,
        cursor,
        isLoading: query.isLoading,
        error: query.error,
        unreadCount: notifications?.filter(n => n && !n?.read_at)?.length,
        markAsRead: markAsReadMutation.mutate,
        refetch: query.refetch,
        fetchNextPage: query.fetchNextPage,
        isFetchingNextPage: query.isFetchingNextPage,
        hasNextPage: query.hasNextPage
    }
}