// @vitest-environment jsdom
/**
 * Mounted tests for the workflow jobs hook: what paging and filter writes put
 * in the address when there is nothing to page or filter from. Only the
 * transport and the navigation hooks are mocked; react-query runs for real.
 */
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { cleanup, renderHook, waitFor } from "@testing-library/react"
import type { ReactNode } from "react"
import { afterEach, beforeEach, expect, it, vi } from "vitest"

import { useWorkflowJobs } from "./use-workflow-jobs"

const mocks = vi.hoisted(() => ({
    search: "",
    push: vi.fn(),
    back: vi.fn(),
    replace: vi.fn(),
    fetchApiJson: vi.fn(),
}))

vi.mock("next/navigation", () => ({
    usePathname: () => "/workflows/w1",
    useRouter: () => ({ push: mocks.push, back: mocks.back, replace: mocks.replace }),
    useSearchParams: () => new URLSearchParams(mocks.search),
}))

vi.mock("sonner", () => ({ toast: { error: vi.fn(), success: vi.fn() }, Toaster: () => null }))

vi.mock("@/lib/api/client", () => ({
    fetchApi: vi.fn(),
    fetchApiJson: mocks.fetchApiJson,
    createIdempotencyKey: () => "idempotency-key",
}))

const renderJobs = () => {
    const queryClient = new QueryClient({
        defaultOptions: { queries: { retry: false, gcTime: 0 }, mutations: { retry: false } },
    })
    const wrapper = ({ children }: { children: ReactNode }) => (
        <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
    )
    return renderHook(() => useWorkflowJobs("w1"), { wrapper })
}

const lastPush = () => mocks.push.mock.calls.at(-1)?.[0] as string

beforeEach(() => {
    mocks.search = ""
    mocks.push.mockClear()
    mocks.back.mockClear()
    mocks.replace.mockClear()
    mocks.fetchApiJson.mockReset().mockResolvedValue({ jobs: [] })
})

afterEach(() => cleanup())

it("cannot page forward when the response advertises no cursor", async () => {
    const { result } = renderJobs()
    await waitFor(() => expect(result.current.isLoading).toBe(false))

    expect(result.current.pagination.goToNextPage()).toBe(false)
    expect(mocks.push).not.toHaveBeenCalled()
})

it("pages forward with the server cursor and drops it when resetting", async () => {
    mocks.search = "tab=jobs&status=FAILED&trigger=MANUAL&cursor=cursor-1"
    mocks.fetchApiJson.mockResolvedValue({ jobs: [], cursor: "cursor-2" })
    const { result, rerender } = renderJobs()
    await waitFor(() => expect(result.current.pagination.hasNextPage).toBe(true))

    expect(result.current.pagination.goToNextPage()).toBe(true)
    expect(lastPush()).toBe("?tab=jobs&status=FAILED&trigger=MANUAL&cursor=cursor-2")

    mocks.search = lastPush().slice(1)
    rerender()
    await waitFor(() => expect(result.current.isLoading).toBe(false))
    expect(result.current.pagination.hasPreviousPage).toBe(true)

    result.current.pagination.resetPagination()
    expect(lastPush()).toBe("?tab=jobs&status=FAILED&trigger=MANUAL")
})

it("clearing filters keeps an open jobs tab and writes nothing else", () => {
    mocks.search = "tab=jobs&status=FAILED&trigger=MANUAL&cursor=cursor-2"
    const { result } = renderJobs()

    result.current.clearAllFilters()

    expect(lastPush()).toBe("?tab=jobs")
})

it("clearing filters outside a tab writes an empty query", () => {
    mocks.search = "status=FAILED&trigger=MANUAL&cursor=cursor-2"
    const { result } = renderJobs()

    result.current.clearAllFilters()

    expect(lastPush()).toBe("?")
})
