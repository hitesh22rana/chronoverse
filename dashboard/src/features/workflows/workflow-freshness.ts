"use client"

import { useCallback, useSyncExternalStore } from "react"
import { useQueryClient, type QueryClient, type QueryFilters } from "@tanstack/react-query"

import { queryKeys } from "@/lib/api/query-keys"

function createWorkflowState() {
    let retired = new Set<string>()
    const listeners = new Set<() => void>()
    const resources = new Map<string, AbortController>()

    return {
        subscribe(listener: () => void) {
            listeners.add(listener)
            return () => { listeners.delete(listener) }
        },
        snapshot: () => retired,
        signal(id: string) {
            if (retired.has(id)) return AbortSignal.abort()
            let resource = resources.get(id)
            if (!resource) {
                resource = new AbortController()
                resources.set(id, resource)
            }
            return resource.signal
        },
        retire(id: string) {
            if (retired.has(id)) return
            retired = new Set(retired).add(id)
            resources.get(id)?.abort()
            resources.delete(id)
            listeners.forEach((listener) => listener())
        },
    }
}

const states = new WeakMap<QueryClient, ReturnType<typeof createWorkflowState>>()

export function useWorkflowRetirement() {
    const client = useQueryClient()
    let state = states.get(client)
    if (!state) {
        state = createWorkflowState()
        states.set(client, state)
    }
    const retired = useSyncExternalStore(state.subscribe, state.snapshot, state.snapshot)
    return { retired, state }
}

export function useWorkflowScope(id: string) {
    const { state, retired } = useWorkflowRetirement()
    const signal = useCallback(() => state.signal(id), [state, id])
    const isCurrent = () => Boolean(id) && !state.snapshot().has(id)
    const available = Boolean(id) && !retired.has(id)
    return {
        available,
        isCurrent,
        signal,
        retire: () => state.retire(id),
        requireCurrent() {
            if (!isCurrent()) throw new DOMException("Workflow is unavailable", "AbortError")
        },
        queryKey: (key: readonly unknown[]) => available ? key : ["unavailable-workflow", ...key],
    }
}

const workflowFamilies = new Set([
    "workflow",
    "workflow-analytics",
    "workflow-jobs",
    "job-details",
    "job-logs",
    "job-logs/search",
])

export function workflowDescendants(id: string): QueryFilters {
    return { predicate: ({ queryKey }) => queryKey[1] === id && workflowFamilies.has(String(queryKey[0])) }
}

export async function refreshQueries(client: QueryClient, filters: QueryFilters, eligible = () => true) {
    await client.cancelQueries(filters)
    if (eligible()) await client.invalidateQueries(filters)
}

export function refreshWorkflowEdit(client: QueryClient, id: string, includeAnalytics: boolean, eligible: () => boolean) {
    return refreshQueries(client, {
        predicate: ({ queryKey: [family, owner], state }) => {
            if (family === "workflows" || (includeAnalytics && family === queryKeys.userAnalytics[0])) return true
            if (owner !== id) return false
            if (family === "workflow" || family === "workflow-jobs") return true
            if (family !== "job-details") return false
            const status = (state.data as { status?: string } | undefined)?.status
            return !status || ["PENDING", "QUEUED", "RUNNING"].includes(status)
        },
    }, eligible)
}
