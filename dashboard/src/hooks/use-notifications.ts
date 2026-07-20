"use client"

import { useInfiniteQuery, useMutation, useQueryClient, type InfiniteData } from "@tanstack/react-query"
import { toast } from "sonner"

import { useUsers } from "@/hooks/use-users"

import { fetchApi, fetchApiJson } from "@/lib/api/client"
import { apiEndpoints, withQuery } from "@/lib/api/endpoints"
import { queryKeys } from "@/lib/api/query-keys"
import type { NotificationsResponse } from "@/lib/api/types"

type UseNotificationsOptions = {
    poll?: boolean
}

export function useNotifications({ poll = false }: UseNotificationsOptions = {}) {
    const { user } = useUsers()
    const queryClient = useQueryClient()

    const query = useInfiniteQuery<NotificationsResponse, Error>({
        queryKey: queryKeys.notifications,
        queryFn: async ({ pageParam }) => {
            const params = new URLSearchParams()
            if (pageParam) {
                params.set("cursor", String(pageParam))
            }

            return fetchApiJson<NotificationsResponse>(
                withQuery(apiEndpoints.notifications, params),
                "failed to fetch notifications",
            )
        },
        initialPageParam: null,
        getNextPageParam: (lastPage) => lastPage?.cursor || null,
        refetchInterval: poll ? 60000 : false,
        refetchIntervalInBackground: false,
        enabled: !!user && user.notification_preference !== "NONE",
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
            const batches: string[][] = []
            for (let i = 0; i < ids.length; i += batchSize) {
                batches.push(ids.slice(i, i + batchSize))
            }

            await Promise.all(batches.map(async (batch) => {
                await fetchApi(apiEndpoints.notifications, "failed to mark notifications as read", {
                    method: "PUT",
                    body: JSON.stringify({ ids: batch }),
                })
            }))

            return ids
        },
        onSuccess: (ids) => {
            const markedIds = new Set(ids)
            queryClient.setQueryData(
                queryKeys.notifications,
                (oldData: InfiniteData<NotificationsResponse> | undefined) => {
                    if (!oldData) return oldData

                    // Remove the notifications from the old data
                    const updatedPages = oldData.pages.map((page) => {
                        const updatedNotifications = page.notifications.filter(
                            (notification) => !markedIds.has(notification.id),
                        )
                        return { ...page, notifications: updatedNotifications }
                    })

                    return {
                        ...oldData,
                        pages: updatedPages,
                    }
                },
            )
        },
        onError: (error) => {
            toast.error(error.message)
        },
    })

    return {
        notifications,
        isLoading: query.isLoading,
        error: query.error,
        refetch: query.refetch,
        fetchNextPage: query.fetchNextPage,
        isFetchingNextPage: query.isFetchingNextPage,
        hasNextPage: query.hasNextPage,
        markAsRead: markAsReadMutation.mutate,
    }
}
