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
    expect(mocks.fetchApiJson).toHaveBeenCalledTimes(1)
    expect(mocks.toast.success).not.toHaveBeenCalled()
    expect(mocks.push).not.toHaveBeenCalled()
    expect(mocks.back).not.toHaveBeenCalled()
    expect(mocks.replace).not.toHaveBeenCalled()
})

it.each([false, true])("an acknowledged edit supersedes an outstanding read (cached=%s)", async (cached) => {
    const old = Promise.withResolvers<Workflow>()
    const fresh = Promise.withResolvers<Workflow>()
    let staleSignal: AbortSignal | undefined

    mocks.fetchApi.mockResolvedValue(new Response(null, { status: 204 }))
    if (cached) mocks.fetchApiJson.mockResolvedValueOnce(workflow())
    mocks.fetchApiJson.mockImplementationOnce((_url, _msg, { signal }: RequestInit) => {
        staleSignal = signal ?? undefined
        return old.promise
    }).mockReturnValueOnce(fresh.promise)
    const { result } = renderUseWorkflowDetails()
    if (cached) {
        await waitFor(() => expect(result.current.workflow?.name).toBe("Nightly container"))
        act(() => { void result.current.refetch() })
    }
    await waitFor(() => expect(staleSignal).toBeDefined())

    const onSaved = vi.fn()
    act(() => {
        result.current.updateWorkflow({ name: "Renamed" } as Parameters<WorkflowDetails["updateWorkflow"]>[0], onSaved)
    })
    await waitFor(() => expect(staleSignal?.aborted).toBe(true))
    await waitFor(() => expect(onSaved).toHaveBeenCalledOnce())
    expect(result.current.isUpdating).toBe(false)
    await act(async () => {
        fresh.resolve(workflow({ name: "Renamed" }))
        old.resolve(workflow({ name: "Superseded response" }))
    })
    await waitFor(() => expect(result.current.workflow?.name).toBe("Renamed"))
    expect(mocks.fetchApiJson).toHaveBeenCalledTimes(cached ? 3 : 2)
})

it("retires a workflow after an ambiguous delete and an authoritative missing response", async () => {
    mocks.fetchApi
        .mockRejectedValueOnce(Object.assign(new Error("gateway error"), { status: 503 }))
        .mockRejectedValueOnce(Object.assign(new Error("workflow missing"), { status: 404 }))
    const { result, queryClient } = renderUseWorkflowDetails()
    await waitFor(() => expect(result.current.workflow?.name).toBe("Nightly container"))
    const refetch = result.current.refetch
    const refetchAnalytics = result.current.refetchAnalytics
    act(() => result.current.deleteWorkflow())
    await waitFor(() => expect(result.current.isWorkflowAvailable).toBe(false))
    act(() => { void refetch(); void refetchAnalytics() })
    expect(mocks.fetchApiJson).toHaveBeenCalledTimes(1)
    expect(queryClient.getQueryData(queryKeys.workflow.detail("w1"))).toBeUndefined()
    expect(mocks.fetchApi).toHaveBeenCalledTimes(2)
    expect(mocks.toast.success).not.toHaveBeenCalled()
})

it.each([
    [404, null, false, null],
    [403, 200, true, "Delete request failed. The workflow is still available"],
    [401, 401, false, null],
    [503, 500, true, "Could not confirm whether the workflow was deleted"],
] as const)("reconciles DELETE %s and scoped GET %s", async (deleteStatus, readStatus, visible, message) => {
    mocks.fetchApi.mockRejectedValueOnce(Object.assign(new Error("delete failed"), { status: deleteStatus }))
    if (readStatus === 200) mocks.fetchApi.mockResolvedValueOnce(new Response("{}"))
    else if (readStatus) mocks.fetchApi.mockRejectedValueOnce(Object.assign(new Error("read failed"), { status: readStatus }))
    const { result } = renderUseWorkflowDetails()
    await waitFor(() => expect(result.current.workflow?.name).toBe("Nightly container"))
    act(() => result.current.deleteWorkflow())
    await waitFor(() => expect(mocks.fetchApi).toHaveBeenCalledTimes(readStatus ? 2 : 1))
    await waitFor(() => expect(result.current.deleteError).toBeTruthy())
    expect(result.current.isWorkflowAvailable).toBe(visible)
    if (message) expect(mocks.toast.error).toHaveBeenCalledWith(message)
    else expect(mocks.toast.error).not.toHaveBeenCalled()
    expect(mocks.toast.success).not.toHaveBeenCalled()
})

it.each([true, false])("suppresses late edit and terminate feedback after delete (success=%s)", async (success) => {
    const edit = Promise.withResolvers<Response>()
    const terminate = Promise.withResolvers<Response>()
    const ok = new Response(null, { status: 204 })
    mocks.fetchApi.mockReturnValueOnce(edit.promise).mockReturnValueOnce(terminate.promise).mockResolvedValueOnce(ok)
    const { result } = renderUseWorkflowDetails()
    await waitFor(() => expect(result.current.workflow).toBeTruthy())
    const onSaved = vi.fn()
    act(() => {
        result.current.updateWorkflow({ name: "Renamed" } as Parameters<WorkflowDetails["updateWorkflow"]>[0], onSaved)
        result.current.terminateWorkflow()
    })
    await waitFor(() => expect(mocks.fetchApi).toHaveBeenCalledTimes(2))
    act(() => result.current.deleteWorkflow())
    await waitFor(() => expect(result.current.isWorkflowAvailable).toBe(false))
    await act(async () => {
        if (success) {
            edit.resolve(ok)
            terminate.resolve(ok)
        } else {
            edit.reject(new Error("late edit error"))
            terminate.reject(new Error("late terminate error"))
        }
    })
    await waitFor(() => expect(result.current.isUpdating || result.current.isTerminating).toBe(false))
    expect(onSaved).not.toHaveBeenCalled()
    expect(mocks.toast.success.mock.calls).toEqual([["workflow deleted successfully"]])
    expect(mocks.toast.error).not.toHaveBeenCalled()
    expect(mocks.fetchApiJson).toHaveBeenCalledTimes(1)
})

it("refreshes only the workflow and job families affected by editing", async () => {
    mocks.fetchApi.mockResolvedValue(new Response(null, { status: 204 }))
    const { result, queryClient } = renderUseWorkflowDetails()
    await waitFor(() => expect(result.current.workflow).toBeTruthy())
    const affected = [queryKeys.userAnalytics, queryKeys.workflow.jobs("w1", "", "", ""), queryKeys.job.detail("w1", "pending")]
    const preserved = [queryKeys.workflow.analytics("w1"), queryKeys.workflow.detail("w2"), queryKeys.job.detail("w1", "terminal")]
    for (const key of affected) {
        queryClient.setQueryDefaults(key, { gcTime: Infinity })
        queryClient.setQueryData(key, { status: "PENDING" })
    }
    for (const key of preserved) {
        queryClient.setQueryDefaults(key, { gcTime: Infinity })
        queryClient.setQueryData(key, { status: "COMPLETED" })
    }
    act(() => result.current.updateWorkflow({ name: "Renamed" } as Parameters<WorkflowDetails["updateWorkflow"]>[0]))
    await waitFor(() => {
        expect(affected.map((key) => queryClient.getQueryState(key)?.isInvalidated)).toEqual([true, true, true])
    })
    expect(preserved.map((key) => queryClient.getQueryState(key)?.isInvalidated)).toEqual([false, false, false])
})

it("does not start a queued edit refresh after retirement intervenes during cancellation", async () => {
    mocks.fetchApi.mockResolvedValue(new Response(null, { status: 204 }))
    const { result, queryClient } = renderUseWorkflowDetails()
    await waitFor(() => expect(result.current.workflow).toBeTruthy())
    const cancelDone = Promise.withResolvers<void>()
    const cancel = queryClient.cancelQueries.bind(queryClient)
    vi.spyOn(queryClient, "cancelQueries").mockImplementationOnce(async (...args) => {
        await cancel(...args)
        await cancelDone.promise
    })
    act(() => result.current.updateWorkflow({ name: "Renamed" } as Parameters<WorkflowDetails["updateWorkflow"]>[0]))
    await waitFor(() => expect(result.current.isUpdating).toBe(false))
    act(() => result.current.deleteWorkflow())
    await waitFor(() => expect(result.current.isWorkflowAvailable).toBe(false))
    await act(async () => { cancelDone.resolve() })
    expect(mocks.fetchApiJson).toHaveBeenCalledTimes(1)
})

it.each(["success", "rejected", "verifying"])("ignores a competing DELETE that is %s after retirement", async (settlement) => {
    const delayed = Promise.withResolvers<Response>()
    const verification = Promise.withResolvers<Response>()
    const ok = new Response(null, { status: 204 })
    mocks.fetchApi.mockReturnValueOnce(delayed.promise)
    if (settlement === "verifying") mocks.fetchApi.mockReturnValueOnce(verification.promise)
    mocks.fetchApi.mockResolvedValueOnce(ok)
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    const wrapper = ({ children }: { children: ReactNode }) => (
        <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
    )
    const { result } = renderHook(() => ({
        delayed: useWorkflowDetails("w1"),
        winning: useWorkflowDetails("w1"),
    }), { wrapper })
    await waitFor(() => expect(result.current.delayed.workflow).toBeTruthy())
    act(() => result.current.delayed.deleteWorkflow())
    await waitFor(() => expect(mocks.fetchApi).toHaveBeenCalledTimes(1))
    if (settlement === "verifying") {
        await act(async () => { delayed.reject(new TypeError("lost DELETE response")) })
        await waitFor(() => expect(mocks.fetchApi).toHaveBeenCalledTimes(2))
    }
    act(() => result.current.winning.deleteWorkflow())
    await waitFor(() => expect(result.current.winning.isWorkflowAvailable).toBe(false))
    await act(async () => {
        if (settlement === "success") delayed.resolve(ok)
        else if (settlement === "rejected") delayed.reject(new Error("late DELETE rejection"))
        else verification.resolve(new Response("{}"))
    })
    await waitFor(() => expect(result.current.delayed.isDeleting).toBe(false))
    expect(mocks.toast.success.mock.calls).toEqual([["workflow deleted successfully"]])
    expect(mocks.toast.error).not.toHaveBeenCalled()
})
