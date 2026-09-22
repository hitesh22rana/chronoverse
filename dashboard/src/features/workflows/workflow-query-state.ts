import type { FetchStatus } from "@tanstack/react-query"

/**
 * Opening-fetch gate for the workflow edit dialog. The detail query serves
 * stale cache first, so the form must stay locked until a refresh settles
 * successfully. `isFetching` alone is not enough: an offline fetch pauses
 * (`fetchStatus: "paused"`, no error) and a failed refetch keeps cached data
 * with `fetchStatus: "idle"`, both of which would unlock stale values.
 */
export function isWorkflowDetailsReady(fetchStatus: FetchStatus, error: unknown): boolean {
    return fetchStatus === "idle" && !error
}
