// @vitest-environment jsdom
// Rejected mutations preserve the cache and location through the real query lifecycle.
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { act, cleanup, renderHook, waitFor } from "@testing-library/react"
import type { ReactNode } from "react"
import { afterEach, beforeEach, expect, it, vi } from "vitest"

import { queryKeys } from "@/lib/api/query-keys"
import type { Workflow } from "./types"
import { useWorkflowDetails } from "./use-workflow-details"

const mocks = vi.hoisted(() => ({
    push: vi.fn(),
    back: vi.fn(),
    replace: vi.fn(),
    fetchApi: vi.fn(),
    fetchApiJson: vi.fn(),
    toast: { error: vi.fn(), success: vi.fn(), warning: vi.fn() },
    workflow: null as unknown,
}))

vi.mock("next/navigation", () => ({
    usePathname: () => "/workflows/w1",
    useRouter: () => ({ push: mocks.push, back: mocks.back, replace: mocks.replace }),
    useSearchParams: () => new URLSearchParams(),
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

function renderUseWorkflowDetails() {
    const queryClient = new QueryClient({
        defaultOptions: { queries: { retry: false, gcTime: 0 }, mutations: { retry: false } },
    })
    const wrapper = ({ children }: { children: ReactNode }) => (
        <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
    )
    return { ...renderHook(() => useWorkflowDetails("w1"), { wrapper }), queryClient }
}

beforeEach(() => {
    mocks.workflow = workflow()
    mocks.push.mockClear()
    mocks.back.mockClear()
    mocks.replace.mockClear()
    mocks.toast.error.mockClear()
    mocks.toast.success.mockClear()
    mocks.fetchApi.mockReset()
    mocks.fetchApiJson.mockReset().mockImplementation(async () => mocks.workflow)
})

afterEach(cleanup)

type WorkflowDetails = ReturnType<typeof useWorkflowDetails>

// A rejected action must not refresh, clear or navigate away from the page.
it.each([
    {
        action: "terminate",
        method: "PATCH",
        requestMessage: "failed to terminate workflow",
        errorMessage: "failed to terminate workflow: 409 conflict",
        run: (details: WorkflowDetails) => details.terminateWorkflow(),
        error: (details: WorkflowDetails) => details.terminateError,
        isPending: (details: WorkflowDetails) => details.isTerminating,
    },
    {
        action: "delete",
        method: "DELETE",
        requestMessage: "failed to delete workflow",
        errorMessage: "failed to delete workflow: 503 unavailable",
        run: (details: WorkflowDetails) => details.deleteWorkflow(),
        error: (details: WorkflowDetails) => details.deleteError,
        isPending: (details: WorkflowDetails) => details.isDeleting,
    },
])("reports a rejected $action and leaves cache and address untouched", async ({ method, requestMessage, errorMessage, run, error, isPending }) => {
    mocks.fetchApi.mockRejectedValue(new Error(errorMessage))
    const { result, queryClient } = renderUseWorkflowDetails()
    await waitFor(() => expect(result.current.workflow?.name).toBe("Nightly container"))

    const invalidateSpy = vi.spyOn(queryClient, "invalidateQueries")
    const removeSpy = vi.spyOn(queryClient, "removeQueries")

    act(() => {
        run(result.current)
    })

    await waitFor(() => expect(mocks.toast.error).toHaveBeenCalledWith(errorMessage))
    expect(mocks.toast.error).toHaveBeenCalledTimes(1)
    expect(mocks.fetchApi).toHaveBeenCalledTimes(1)
    const [url, message, init] = mocks.fetchApi.mock.calls[0] as [string, string, RequestInit]
    expect(url).toBe("/workflows/w1")
    expect(message).toBe(requestMessage)
    expect(init.method).toBe(method)
    expect(error(result.current)?.message).toBe(errorMessage)
    expect(isPending(result.current)).toBe(false)

    expect(invalidateSpy).not.toHaveBeenCalled()
    expect(removeSpy).not.toHaveBeenCalled()
    expect(queryClient.getQueryData(queryKeys.workflow.detail("w1"))).toEqual(mocks.workflow)
    expect(mocks.push).not.toHaveBeenCalled()
    expect(mocks.back).not.toHaveBeenCalled()
})
