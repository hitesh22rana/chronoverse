"use client"

import { useInfiniteQuery, useMutation, useQueryClient, type InfiniteData } from "@tanstack/react-query"
import { toast } from "sonner"

import { useUsers } from "@/features/users/use-users"

import { fetchApi, fetchApiJson } from "@/lib/api/client"
import { apiEndpoints, withQuery } from "@/lib/api/endpoints"
import { queryRefetchIntervals } from "@/lib/api/query-policy"
import { queryKeys } from "@/lib/api/query-keys"
import type { NotificationsResponse } from "@/features/notifications/types"
import {
    batchNotificationIds,
    flattenNotifications,
    removeNotificationsFromPages,
} from "@/features/notifications/notification-data"
import { canReceiveNotifications } from "@/features/users/user-preferences"

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
        refetchInterval: poll ? queryRefetchIntervals.notifications : false,
        refetchIntervalInBackground: false,
        enabled: canReceiveNotifications(user),
    })

    if (query.error instanceof Error) {
        toast.error(query.error.message)
    }

    const notifications = flattenNotifications(query.data?.pages)

    const markAsReadMutation = useMutation({
        mutationFn: async (ids: string[]) => {
            await Promise.all(batchNotificationIds(ids).map(async (batch) => {
                await fetchApi(apiEndpoints.notifications, "failed to mark notifications as read", {
                    method: "PUT",
                    body: JSON.stringify({ ids: batch }),
                })
            }))

            return ids
        },
        onSuccess: (ids) => {
            queryClient.setQueryData(
                queryKeys.notifications,
                (oldData: InfiniteData<NotificationsResponse> | undefined) => {
                    if (!oldData) return oldData

                    return {
                        ...oldData,
                        pages: removeNotificationsFromPages(oldData.pages, ids),
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
