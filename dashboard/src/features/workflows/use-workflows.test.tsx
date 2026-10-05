// @vitest-environment jsdom
/**
 * Mounted tests for the workflow list hook: how the address becomes a list
 * request, how paging/search/filter navigation is written back, and what a
 * create does to the cache, the URL and the user-visible result.
 *
 * Boundaries only: the transport (`@/lib/api/client`), the Next.js navigation
 * hooks and `sonner`. react-query runs for real, so the assertions are about the
 * requests the hook issues and the navigation it performs.
 */
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { act, cleanup, renderHook, waitFor } from "@testing-library/react"
import type { ReactNode } from "react"
import { afterEach, beforeEach, expect, it, vi } from "vitest"

import type { CreateWorkflowPayload, Workflow } from "./types"
import { useWorkflows } from "./use-workflows"

const mocks = vi.hoisted(() => ({
    pathname: "/",
    search: "",
    push: vi.fn(),
    back: vi.fn(),
    replace: vi.fn(),
    fetchApi: vi.fn(),
    fetchApiJson: vi.fn(),
    toast: { error: vi.fn(), success: vi.fn(), warning: vi.fn() },
    list: null as unknown,
    holdCreate: false,
    releaseCreate: null as null | (() => void),
}))

vi.mock("next/navigation", () => ({
    usePathname: () => mocks.pathname,
    useRouter: () => ({ push: mocks.push, back: mocks.back, replace: mocks.replace }),
    useSearchParams: () => new URLSearchParams(mocks.search),
}))

vi.mock("sonner", () => ({ toast: mocks.toast, Toaster: () => null }))

vi.mock("@/lib/api/client", () => ({
    fetchApi: mocks.fetchApi,
    fetchApiJson: mocks.fetchApiJson,
    createIdempotencyKey: () => "idempotency-key",
}))

const workflow = (overrides: Partial<Workflow> = {}): Workflow => ({
    id: "w1",
    name: "Nightly container",
    kind: "CONTAINER",
    payload: JSON.stringify({ image: "alpine:latest" }),
    build_status: "COMPLETED",
    interval: 15,
    max_consecutive_job_failures_allowed: 3,
    log_retention: true,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-02T00:00:00Z",
    ...overrides,
})

const payload: CreateWorkflowPayload = {
    name: "Nightly container",
    kind: "CONTAINER",
    payload: JSON.stringify({ image: "alpine:latest" }),
    interval: 15,
    max_consecutive_job_failures_allowed: 3,
    log_retention: true,
}

/** The mocked transport records `[url, errorMessage, init]` per call. */
const recordedWrite = () => mocks.fetchApi.mock.calls.at(-1) as [string, string, RequestInit]
const listRequests = () => mocks.fetchApiJson.mock.calls.map((call) => String(call[0]))
const lastListRequest = () => listRequests().at(-1)
const lastPush = () => mocks.push.mock.calls.at(-1)?.[0] as string

function renderUseWorkflows(search = "", options: { poll?: boolean } = {}) {
    mocks.search = search
    const queryClient = new QueryClient({
        defaultOptions: { queries: { retry: false, gcTime: 0 }, mutations: { retry: false } },
    })
    const wrapper = ({ children }: { children: ReactNode }) => (
        <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
    )
    return {
        ...renderHook(() => useWorkflows(options), { wrapper }),
        queryClient,
    }
}

beforeEach(() => {
    mocks.pathname = "/"
    mocks.search = ""
    mocks.list = { workflows: [workflow()] }
    mocks.push.mockClear()
    mocks.back.mockClear()
    mocks.replace.mockClear()
    mocks.toast.error.mockClear()
    mocks.toast.success.mockClear()
    mocks.holdCreate = false
    mocks.releaseCreate = null
    mocks.fetchApi.mockReset().mockImplementation(async () => {
        if (mocks.holdCreate) {
            await new Promise<void>((resolve) => {
                mocks.releaseCreate = resolve
            })
        }
        return { ok: true, status: 201 }
    })
    mocks.fetchApiJson.mockReset().mockImplementation(async () => mocks.list)
})

afterEach(cleanup)

it("asks for the unfiltered list and reports what came back", async () => {
    mocks.list = { workflows: [workflow(), workflow({ id: "w2", name: "API heartbeat" })], cursor: "cursor-2" }
    const { result } = renderUseWorkflows()

    await waitFor(() => expect(result.current.workflows).toHaveLength(2))
    expect(lastListRequest()).toBe("/workflows")
    expect(result.current.pagination.hasNextPage).toBe(true)
    expect(result.current.pagination.hasPreviousPage).toBe(false)
    expect(result.current.pagination.currentPage).toBe("first")
    expect(result.current.searchQuery).toBe("")
    // A fresh QueryClient reports no request in flight once the list resolved.
    expect(result.current.isLoading).toBe(false)
    expect(mocks.toast.error).not.toHaveBeenCalled()
})

it.each([
    ["terminated", "status=TERMINATED", "/workflows?terminated=true"],
    ["a build status", "status=COMPLETED", "/workflows?build_status=COMPLETED"],
    ["a kind", "kind=HEARTBEAT", "/workflows?kind=HEARTBEAT"],
    ["an interval range", "interval_min=5&interval_max=60", "/workflows?interval_min=5&interval_max=60"],
    ["everything", "cursor=c1&query=night&status=TERMINATED&kind=CONTAINER&interval_min=5", "/workflows?cursor=c1&query=night&terminated=true&kind=CONTAINER&interval_min=5"],
])("translates %s from the address into the list request", async (_label, search, expected) => {
    renderUseWorkflows(search)

    await waitFor(() => expect(lastListRequest()).toBe(expected))
})

it.each([
    ["interval_min=0"],
    ["interval_min=-5"],
    ["interval_min=10081"],
    ["interval_min=five"],
])("drops an unusable interval bound (%s) instead of passing it on", async (search) => {
    renderUseWorkflows(search)

    await waitFor(() => expect(lastListRequest()).toBe("/workflows"))
})

it("ignores list filters while the address belongs to another page", async () => {
    mocks.pathname = "/workflows/w1"
    renderUseWorkflows("status=TERMINATED&kind=HEARTBEAT&cursor=c1")

    await waitFor(() => expect(lastListRequest()).toBe("/workflows"))
})

it("reports a failed list load and keeps the collection empty", async () => {
    mocks.fetchApiJson.mockRejectedValue(new Error("failed to fetch workflows: offline"))
    const { result } = renderUseWorkflows()

    await waitFor(() =>
        expect(mocks.toast.error).toHaveBeenCalledWith("failed to fetch workflows: offline"),
    )
    expect(result.current.workflows).toEqual([])
})

it("walks the list forward and back through the cursor", async () => {
    mocks.list = { workflows: [workflow()], cursor: "cursor-2" }
    const { result } = renderUseWorkflows("query=night&cursor=c1")
    await waitFor(() => expect(result.current.pagination.hasNextPage).toBe(true))
    expect(result.current.pagination.hasPreviousPage).toBe(true)
    expect(result.current.pagination.currentPage).toBe("paginated")

    act(() => {
        result.current.pagination.goToNextPage()
    })
    expect(lastPush()).toBe("?query=night&cursor=cursor-2")

    act(() => {
        result.current.pagination.goToPreviousPage()
    })
    expect(mocks.back).toHaveBeenCalledTimes(1)

    act(() => {
        result.current.pagination.resetPagination()
    })
    expect(lastPush()).toBe("?query=night")
})

it("refuses to page forward without a cursor", async () => {
    const { result } = renderUseWorkflows()
    await waitFor(() => expect(result.current.workflows).toHaveLength(1))

    let moved: boolean | undefined
    act(() => {
        moved = result.current.pagination.goToNextPage()
    })

    expect(moved).toBe(false)
    expect(mocks.push).not.toHaveBeenCalled()
})

it("resets paging when the search text changes and removes it when cleared", async () => {
    const { result } = renderUseWorkflows("cursor=c1")
    await waitFor(() => expect(lastListRequest()).toBe("/workflows?cursor=c1"))

    act(() => {
        result.current.updateSearchQuery("nightly")
    })
    expect(lastPush()).toBe("?query=nightly")

    act(() => {
        result.current.updateSearchQuery("")
    })
    expect(lastPush()).toBe("?")
})

it("writes applied filters over the address and resets the cursor", async () => {
    const { result } = renderUseWorkflows("cursor=c1&query=night&status=FAILED")
    await waitFor(() => expect(lastListRequest()).toBe("/workflows?cursor=c1&query=night&build_status=FAILED"))

    act(() => {
        result.current.applyAllFilters({ status: "COMPLETED", kind: "HEARTBEAT", intervalMin: "5", intervalMax: "60" })
    })
    expect(lastPush()).toBe("?query=night&status=COMPLETED&kind=HEARTBEAT&interval_min=5&interval_max=60")

    // "ALL" is the unset choice for each select, and bad intervals are dropped.
    act(() => {
        result.current.applyAllFilters({ status: "ALL", kind: "ALL", intervalMin: "0", intervalMax: "" })
    })
    expect(lastPush()).toBe("?query=night")
})

it("clears every filter but keeps the search text", async () => {
    const { result } = renderUseWorkflows("query=night&status=FAILED&kind=HEARTBEAT&interval_min=5")
    await waitFor(() => expect(result.current.statusFilter).toBe("FAILED"))
    expect(result.current.intervalMin).toBe("5")

    act(() => {
        result.current.clearAllFilters()
    })

    expect(lastPush()).toBe("?query=night")
})

it("re-requests the list on demand", async () => {
    const { result } = renderUseWorkflows("status=FAILED")
    await waitFor(() => expect(lastListRequest()).toBe("/workflows?build_status=FAILED"))
    const before = listRequests().length

    await act(async () => {
        await result.current.refetch()
    })

    expect(listRequests()).toHaveLength(before + 1)
    expect(lastListRequest()).toBe("/workflows?build_status=FAILED")
})

it("polls an idle list slowly and a building list quickly", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    try {
        mocks.list = { workflows: [workflow({ build_status: "COMPLETED" })] }
        const idle = renderUseWorkflows("", { poll: true })
        await waitFor(() => expect(idle.result.current.workflows).toHaveLength(1))
        const idleRequests = listRequests().length

        await act(async () => {
            await vi.advanceTimersByTimeAsync(59_000)
        })
        expect(listRequests()).toHaveLength(idleRequests)

        await act(async () => {
            await vi.advanceTimersByTimeAsync(1_000)
        })
        expect(listRequests()).toHaveLength(idleRequests + 1)
        idle.unmount()

        mocks.list = { workflows: [workflow({ build_status: "STARTED" })] }
        const active = renderUseWorkflows("", { poll: true })
        await waitFor(() => expect(active.result.current.workflows).toHaveLength(1))
        const activeRequests = listRequests().length

        await act(async () => {
            await vi.advanceTimersByTimeAsync(5_000)
        })
        expect(listRequests()).toHaveLength(activeRequests + 1)
        active.unmount()
    } finally {
        vi.useRealTimers()
    }
})

it("creates a workflow, refreshes the list and reports it", async () => {
    mocks.holdCreate = true
    const { result } = renderUseWorkflows("cursor=c1")
    await waitFor(() => expect(result.current.workflows).toHaveLength(1))
    const requestsBeforeCreate = listRequests().length

    const onCreated = vi.fn()
    act(() => {
        result.current.createWorkflow(payload, onCreated)
    })

    await waitFor(() => expect(mocks.fetchApi).toHaveBeenCalledTimes(1))
    const [url, , init] = recordedWrite()
    expect(url).toBe("/workflows")
    expect(init.method).toBe("POST")
    expect((init.headers as Record<string, string>)["Idempotency-Key"]).toBe("idempotency-key")
    expect(JSON.parse(String(init.body))).toEqual(payload)

    expect(result.current.isCreating).toBe(true)

    await act(async () => {
        mocks.releaseCreate?.()
    })

    await waitFor(() => expect(result.current.isCreating).toBe(false))
    expect(mocks.toast.success).toHaveBeenCalledWith("workflow created successfully")
    expect(onCreated).toHaveBeenCalledTimes(1)
    // A new workflow belongs on the first page, so the cursor is dropped.
    expect(lastPush()).toBe("?")
    await waitFor(() => expect(listRequests().length).toBe(requestsBeforeCreate + 1))
})

it("keeps the caller informed when a create is rejected", async () => {
    mocks.fetchApi.mockRejectedValue(new Error("failed to create workflow: 422 invalid"))
    const { result } = renderUseWorkflows()
    await waitFor(() => expect(result.current.workflows).toHaveLength(1))

    const onCreated = vi.fn()
    act(() => {
        result.current.createWorkflow(payload, onCreated)
    })

    await waitFor(() =>
        expect(mocks.toast.error).toHaveBeenCalledWith("failed to create workflow: 422 invalid"),
    )
    expect(onCreated).not.toHaveBeenCalled()
    expect(mocks.push).not.toHaveBeenCalled()
    expect(result.current.isCreating).toBe(false)
})