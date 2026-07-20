"use client"

import { useQuery } from "@tanstack/react-query"

import { fetchApiJson } from "@/lib/api/client"
import { apiEndpoints } from "@/lib/api/endpoints"
import { queryKeys } from "@/lib/api/query-keys"
import type { UserAnalytics } from "@/features/analytics/types"
import {
    normalizeUserAnalytics,
    type UserAnalyticsPayload,
} from "@/features/analytics/analytics-data"

export function useUserAnalytics(enabled: boolean) {
    return useQuery<UserAnalytics, Error>({
        queryKey: queryKeys.userAnalytics,
        queryFn: async () => normalizeUserAnalytics(
            await fetchApiJson<UserAnalyticsPayload>(
                apiEndpoints.analytics.user,
                "failed to fetch analytics",
            ),
        ),
        enabled,
        staleTime: 5 * 60 * 1000,
        refetchOnWindowFocus: false,
    })
}
