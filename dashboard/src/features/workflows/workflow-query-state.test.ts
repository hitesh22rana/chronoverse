import { QueryClient, QueryObserver, onlineManager } from "@tanstack/query-core"
import { afterEach, expect, it } from "vitest"
import { isWorkflowDetailsReady } from "./workflow-query-state"

const tick = (ms = 50) => new Promise<void>((resolve) => setTimeout(resolve, ms))

afterEach(() => {
    onlineManager.setOnline(true)
})

function observe<T>(client: QueryClient, key: string[], queryFn: () => Promise<T>) {
    const observer = new QueryObserver(client, { queryKey: key, queryFn, retry: false })
    const unsubscribe = observer.subscribe(() => {})
    return { snapshot: () => observer.getCurrentResult(), unsubscribe }
}

it("stays locked while the opening fetch is paused offline with stale cache", async () => {
    const client = new QueryClient()
    client.setQueryData(["w"], { v: 1 })
    onlineManager.setOnline(false)
    try {
        const { snapshot, unsubscribe } = observe(client, ["w"], async () => ({ v: 2 }))
        await tick(100)
        const state = snapshot()
        expect(state.fetchStatus).toBe("paused")
        expect(state.isFetching).toBe(false)
        expect(state.error).toBeNull()
        expect(state.data).toEqual({ v: 1 })
        expect(isWorkflowDetailsReady(state.fetchStatus, state.error)).toBe(false)
        unsubscribe()
    } finally {
        onlineManager.setOnline(true)
        client.clear()
    }
})

it("stays locked while fetching and on failure, unlocks on success", async () => {
    const client = new QueryClient()
    try {
        let resolveFetch!: (_value: number) => void
        const pending = new Promise<number>((resolve) => { resolveFetch = resolve })
        const fetching = observe(client, ["fetching"], () => pending)
        expect(isWorkflowDetailsReady(fetching.snapshot().fetchStatus, null)).toBe(false)
        resolveFetch(1)
        await tick(100)
        expect(isWorkflowDetailsReady(fetching.snapshot().fetchStatus, fetching.snapshot().error)).toBe(true)
        fetching.unsubscribe()

        const failing = observe(client, ["failing"], async () => { throw new Error("boom") })
        await tick(100)
        const failed = failing.snapshot()
        expect(failed.error).not.toBeNull()
        expect(isWorkflowDetailsReady(failed.fetchStatus, failed.error)).toBe(false)
        failing.unsubscribe()
    } finally {
        client.clear()
    }
})

it("unlocks immediately with fresh cache and no fetch", () => {
    const client = new QueryClient()
    try {
        client.setQueryData(["fresh"], { v: 1 })
        const observer = new QueryObserver(client, {
            queryKey: ["fresh"],
            queryFn: async () => ({ v: 2 }),
            staleTime: Infinity,
            retry: false,
        })
        const unsubscribe = observer.subscribe(() => {})
        const state = observer.getCurrentResult()
        expect(state.fetchStatus).toBe("idle")
        expect(isWorkflowDetailsReady(state.fetchStatus, state.error)).toBe(true)
        unsubscribe()
    } finally {
        client.clear()
    }
})
