"use client"

import { useQuery } from "@tanstack/react-query"

import { fetchApiJson } from "@/lib/api/client"
import { apiEndpoints } from "@/lib/api/endpoints"
import { queryStaleTimes } from "@/lib/api/query-policy"
import { queryKeys } from "@/lib/api/query-keys"
import type { UserAnalytics } from "@/features/analytics/types"
import {
    normalizeUserAnalytics,
    type UserAnalyticsPayload,
} from "@/features/analytics/analytics-data"

import { useWorkflowRetirement } from "@/features/workflows/workflow-freshness"

export function useUserAnalytics(enabled: boolean) {
    const { retired } = useWorkflowRetirement()
    const query = useQuery<UserAnalytics, Error>({
        queryKey: queryKeys.userAnalytics,
        queryFn: async ({ signal }) => normalizeUserAnalytics(
            await fetchApiJson<UserAnalyticsPayload>(
                apiEndpoints.analytics.user,
                "failed to fetch analytics",
                { signal },
            ),
        ),
        enabled,
        staleTime: queryStaleTimes.analytics,
        refetchOnWindowFocus: false,
    })
    const data = query.data
    if (!data || !retired.size) return query
    return {
        ...query,
        data: {
            ...data,
            top_workflows: data.top_workflows.map((item) => retired.get(item.workflow_id.toLowerCase())
                ? { ...item, workflow_name: "Deleted workflow" }
                : item),
        },
    }
}
