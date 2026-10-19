// @vitest-environment jsdom
// Real query lifecycle; transport, navigation and toast boundaries are mocked.
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { act, cleanup, renderHook, waitFor } from "@testing-library/react"
import type { ReactNode } from "react"
import { afterEach, beforeEach, expect, it, vi } from "vitest"

import type { Job } from "@/features/jobs/types"
import { useWorkflowJobs } from "./use-workflow-jobs"

const mocks = vi.hoisted(() => ({
    pathname: "/workflows/w1",
    search: "",
    push: vi.fn(),
    back: vi.fn(),
    replace: vi.fn(),
    fetchApi: vi.fn(),
    fetchApiJson: vi.fn(),
    toast: { error: vi.fn(), success: vi.fn(), warning: vi.fn() },
    jobsResponse: null as unknown,
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

const job = (overrides: Partial<Job> = {}): Job => ({
    id: "j1",
    workflow_id: "w1",
    status: "COMPLETED",
    trigger: "AUTOMATIC",
    scheduled_at: "2026-01-01T00:00:00Z",
    started_at: "2026-01-01T00:00:01Z",
    completed_at: "2026-01-01T00:00:09Z",
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:09Z",
    ...overrides,
})

function renderUseWorkflowJobs(search: string) {
    mocks.search = search
    const queryClient = new QueryClient({
        defaultOptions: { queries: { retry: false, gcTime: 0 }, mutations: { retry: false } },
    })
    const wrapper = ({ children }: { children: ReactNode }) => (
        <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
    )
    return renderHook(() => useWorkflowJobs("w1"), { wrapper })
}

/** The mocked transport records `[url, errorMessage]` per list request. */
const listRequests = () => mocks.fetchApiJson.mock.calls.map((call) => String(call[0]))
const lastListRequest = () => listRequests().at(-1)
const lastPush = () => mocks.push.mock.calls.at(-1)?.[0] as string

beforeEach(() => {
    mocks.pathname = "/workflows/w1"
    mocks.search = ""
    mocks.jobsResponse = { jobs: [job()] }
    mocks.push.mockClear()
    mocks.back.mockClear()
    mocks.replace.mockClear()
    mocks.toast.error.mockClear()
    mocks.toast.success.mockClear()
    mocks.fetchApi.mockReset()
    mocks.fetchApiJson.mockReset().mockImplementation(async () => mocks.jobsResponse)
})

afterEach(cleanup)

it("refuses to page forward when the response offers no next cursor", async () => {
    const { result } = renderUseWorkflowJobs("tab=jobs")

    // The response arrived and advertised no next page, so the guard is what
    // stops the navigation, not an unfinished request.
    await waitFor(() => expect(result.current.jobs).toHaveLength(1))
    expect(result.current.pagination.hasNextPage).toBe(false)

    let moved: boolean | undefined
    act(() => {
        moved = result.current.pagination.goToNextPage()
    })

    expect(moved).toBe(false)
    expect(mocks.push).not.toHaveBeenCalled()
})

it("pages forward onto the cursor the response hands back and keeps the open tab", async () => {
    mocks.jobsResponse = { jobs: [job()], cursor: "cursor-2" }
    const { result } = renderUseWorkflowJobs("tab=jobs")
    await waitFor(() => expect(result.current.pagination.hasNextPage).toBe(true))

    let moved: boolean | undefined
    act(() => {
        moved = result.current.pagination.goToNextPage()
    })

    expect(moved).toBe(true)
    expect(mocks.push).toHaveBeenCalledTimes(1)
    expect(lastPush()).toBe("?tab=jobs&cursor=cursor-2")
})

it("hands a previous-page request back to browser history", async () => {
    const { result } = renderUseWorkflowJobs("tab=jobs&cursor=cursor-2")

    await waitFor(() => expect(result.current.jobs).toHaveLength(1))
    expect(result.current.pagination.hasPreviousPage).toBe(true)
    expect(lastListRequest()).toBe("/workflows/w1/jobs?cursor=cursor-2")

    let moved: boolean | undefined
    act(() => {
        moved = result.current.pagination.goToPreviousPage()
    })

    expect(moved).toBe(true)
    expect(mocks.back).toHaveBeenCalledTimes(1)
    expect(mocks.push).not.toHaveBeenCalled()
})

it("resets pagination by dropping only the cursor from the address", async () => {
    const { result } = renderUseWorkflowJobs("tab=jobs&cursor=cursor-2&status=FAILED&trigger=MANUAL")

    await waitFor(() => expect(result.current.jobs).toHaveLength(1))
    expect(result.current.pagination.hasPreviousPage).toBe(true)
    expect(lastListRequest()).toBe("/workflows/w1/jobs?cursor=cursor-2&status=FAILED&trigger=MANUAL")

    act(() => {
        result.current.pagination.resetPagination()
    })

    // Filters and the open tab must survive a return to the first page.
    expect(lastPush()).toBe("?tab=jobs&status=FAILED&trigger=MANUAL")
})

it("clears filters without inventing a tab when the address carries none", async () => {
    const { result } = renderUseWorkflowJobs("status=FAILED&trigger=MANUAL")

    await waitFor(() => expect(result.current.jobs).toHaveLength(1))

    act(() => {
        result.current.clearAllFilters()
    })

    // Nothing pinned the address to a tab, so clearing must drop every filter
    // and leave an empty query string rather than fabricate a tab parameter.
    expect(mocks.push).toHaveBeenCalledTimes(1)
    expect(lastPush()).toBe("?")
})
