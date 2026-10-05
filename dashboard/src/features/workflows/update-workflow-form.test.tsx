// @vitest-environment jsdom
/**
 * Mounted tests for the workflow edit dialog: opening-fetch gating and the
 * initialization latch that must never overwrite dirty inputs.
 *
 * Boundaries only: the transport (`@/lib/api/client`), the Next.js navigation
 * hooks, and `sonner`. The dialog, react-hook-form, zod validation, react-query
 * and the Radix primitives all run for real, so the assertions are about what a
 * user can see and edit, and about the PUT the form actually sends.
 */
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { afterEach, beforeEach, expect, it, vi } from "vitest"

import type { Workflow } from "./types"
import { UpdateWorkflowDialog } from "./update-workflow-dialog"

const mocks = vi.hoisted(() => ({
    fetchApi: vi.fn(),
    fetchApiJson: vi.fn(),
    toast: { error: vi.fn(), success: vi.fn(), warning: vi.fn() },
    push: vi.fn(),
    replace: vi.fn(),
    workflow: null as unknown,
    updateCalls: [] as { url: string, init: RequestInit | undefined }[],
    holdUpdates: false,
    releaseUpdate: null as null | (() => void),
}))

vi.mock("next/navigation", () => ({
    useRouter: () => ({ push: mocks.push, replace: mocks.replace }),
    usePathname: () => "/workflows/w1",
    useSearchParams: () => new URLSearchParams(),
}))

vi.mock("sonner", () => ({ toast: mocks.toast, Toaster: () => null }))

vi.mock("@/lib/api/client", () => ({
    fetchApi: mocks.fetchApi,
    fetchApiJson: mocks.fetchApiJson,
    createIdempotencyKey: () => "idempotency-key",
}))

const containerWorkflow = (overrides: Partial<Workflow> = {}): Workflow => ({
    id: "w1",
    name: "Nightly container",
    kind: "CONTAINER",
    payload: JSON.stringify({
        image: "alpine:latest",
        cmd: ["sh", "-c", "echo hi"],
        env: { MODE: "prod" },
        timeout: "30s",
    }),
    build_status: "COMPLETED",
    interval: 15,
    max_consecutive_job_failures_allowed: 3,
    log_retention: true,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-02T00:00:00Z",
    ...overrides,
})

const heartbeatWorkflow = (overrides: Partial<Workflow> = {}): Workflow => ({
    id: "w1",
    name: "API heartbeat",
    kind: "HEARTBEAT",
    payload: JSON.stringify({
        endpoint: "https://example.com/health",
        expected_status_code: 204,
        headers: { "X-Token": "secret" },
        timeout: "10s",
    }),
    build_status: "COMPLETED",
    interval: 5,
    max_consecutive_job_failures_allowed: 4,
    log_retention: false,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-02T00:00:00Z",
    ...overrides,
})

function renderDialog(options: { workflowId?: string, seed?: Workflow } = {}) {
    const queryClient = new QueryClient({
        defaultOptions: { queries: { retry: false, gcTime: 0 }, mutations: { retry: false } },
    })
    if (options.seed) {
        // Warm but stale cache, as when the workflow list loaded this row more
        // than the detail query freshness window ago.
        queryClient.setQueryData(["workflow", options.workflowId ?? "w1"], options.seed, {
            updatedAt: Date.now() - 60_000,
        })
    }
    const onOpenChange = vi.fn()
    const view = render(
        <QueryClientProvider client={queryClient}>
            <UpdateWorkflowDialog workflowId={options.workflowId ?? "w1"} open onOpenChange={onOpenChange} />
        </QueryClientProvider>,
    )
    return { ...view, onOpenChange, queryClient }
}

/** Runs the 5s active-build poll the detail query schedules. */
const poll = async () => {
    await act(async () => {
        await vi.advanceTimersByTimeAsync(5000)
    })
}

const lockedNotice = () => screen.queryByText(/Couldn't load the latest workflow data/)
const saveButton = () => screen.getByRole("button", { name: /Save changes|Saving/ })
const activeTab = () => screen.getByRole("tab", { selected: true }).textContent?.replace(/\s+/g, " ").trim()

/** jsdom implements none of these; `installDomStubs` stands in for them. */
const pointerCaptureMembers = [
    "hasPointerCapture",
    "setPointerCapture",
    "releasePointerCapture",
    "scrollIntoView",
] as const

/** Captured before the first stub is installed, so `afterEach` can undo it. */
const originalPointerCaptureDescriptors = pointerCaptureMembers.map((member) =>
    Object.getOwnPropertyDescriptor(Element.prototype, member)
)

beforeEach(() => {
    mocks.workflow = containerWorkflow()
    mocks.updateCalls = []
    mocks.holdUpdates = false
    mocks.releaseUpdate = null
    mocks.fetchApiJson.mockReset().mockImplementation(async () => mocks.workflow)
    mocks.fetchApi.mockReset().mockImplementation(async (url: string, _message: string, init?: RequestInit) => {
        mocks.updateCalls.push({ url, init })
        if (mocks.holdUpdates) {
            await new Promise<void>((resolve) => {
                mocks.releaseUpdate = resolve
            })
        }
        return { ok: true, status: 200 }
    })
    mocks.push.mockClear()
    mocks.replace.mockClear()
    mocks.toast.error.mockClear()
    mocks.toast.success.mockClear()
    mocks.toast.warning.mockClear()
    installDomStubs()
})

afterEach(() => {
    vi.useRealTimers()
    cleanup()
    vi.unstubAllGlobals()
    restoreDomStubs()
})

it("keeps the editor locked until the opening fetch settles, then loads stored values", async () => {
    let releaseDetail: (() => void) | null = null
    mocks.fetchApiJson.mockImplementation(async () => {
        await new Promise<void>((resolve) => {
            releaseDetail = resolve
        })
        return mocks.workflow
    })

    renderDialog()

    expect(await screen.findByText("Update workflow")).toBeTruthy()
    // Cached values may already be on screen before the refresh settles; the
    // form must not be editable until the opening fetch resolves.
    expect(screen.queryByLabelText("Name")).toBeNull()
    expect(lockedNotice()).toBeNull()

    await act(async () => {
        releaseDetail?.()
    })

    expect(await screen.findByLabelText("Name")).toHaveProperty("value", "Nightly container")
    expect(screen.getByLabelText("Image")).toHaveProperty("value", "alpine:latest")
    expect(screen.getByLabelText("Command argument 1")).toHaveProperty("value", "sh")
    expect(screen.getByLabelText("Command argument 3")).toHaveProperty("value", "echo hi")
    expect(screen.getByLabelText("Environment variable 1")).toHaveProperty("value", "MODE=prod")
    expect(screen.getByLabelText("Timeout (optional)")).toHaveProperty("value", "30s")
    expect(screen.getByLabelText("Interval (minutes)")).toHaveProperty("value", "15")
    expect(screen.getByLabelText("Max consecutive failures allowed")).toHaveProperty("value", "3")
    expect(activeTab()).toBe("Configuration")
})

it("refuses to edit stale cached values when the opening fetch fails and recovers on retry", async () => {
    mocks.workflow = null
    mocks.fetchApiJson.mockImplementation(async () => {
        if (mocks.workflow === null) throw new Error("failed to fetch workflow details: offline")
        return mocks.workflow
    })

    // The list view already loaded this workflow, so the dialog renders cached
    // values immediately and refreshes behind them.
    renderDialog({ seed: containerWorkflow() })

    expect(await screen.findByText("Update workflow")).toBeTruthy()
    await waitFor(() => expect(mocks.fetchApiJson).toHaveBeenCalled())
    expect(screen.queryByLabelText("Name")).toBeNull()

    expect(await screen.findByText(/Couldn't load the latest workflow data/)).toBeTruthy()
    expect(screen.queryByLabelText("Name")).toBeNull()
    expect(screen.queryByRole("button", { name: /Save changes/ })).toBeNull()

    await act(async () => {
        mocks.workflow = containerWorkflow({ updated_at: "2026-01-03T00:00:00Z", name: "Recovered container" })
        fireEvent.click(screen.getByRole("button", { name: "Retry" }))
    })

    expect(await screen.findByLabelText("Name")).toHaveProperty("value", "Recovered container")
})

it("keeps an already unlocked editor open when a later background refresh fails", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    mocks.workflow = containerWorkflow({ build_status: "STARTED" })
    mocks.fetchApiJson.mockImplementation(async () => {
        if (mocks.workflow === null) throw new Error("failed to fetch workflow details: offline")
        return mocks.workflow
    })

    renderDialog()
    expect(await screen.findByLabelText("Name")).toHaveProperty("value", "Nightly container")

    // The opening fetch already succeeded, so later polling failures must not
    // re-lock or blank the editor the user is working in.
    mocks.workflow = null
    await poll()

    expect(screen.getByLabelText("Name")).toHaveProperty("value", "Nightly container")
    expect(lockedNotice()).toBeNull()
})

it("re-initializes a pristine form when a newer workflow version arrives", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    mocks.workflow = containerWorkflow({ build_status: "STARTED" })
    renderDialog()
    expect(await screen.findByLabelText("Name")).toHaveProperty("value", "Nightly container")

    mocks.workflow = containerWorkflow({
        build_status: "STARTED",
        name: "Renamed elsewhere",
        interval: 45,
        updated_at: "2026-01-05T00:00:00Z",
    })
    await poll()

    await waitFor(() => expect(screen.getByLabelText("Name")).toHaveProperty("value", "Renamed elsewhere"))
    expect(screen.getByLabelText("Interval (minutes)")).toHaveProperty("value", "45")
})

it("never overwrites dirty inputs when a newer workflow version arrives", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    mocks.workflow = containerWorkflow({ build_status: "STARTED" })
    renderDialog()
    const name = await screen.findByLabelText("Name")
    const image = screen.getByLabelText("Image")

    await act(async () => {
        fireEvent.change(name, { target: { value: "My local edit" } })
        fireEvent.change(image, { target: { value: "ubuntu:24.04" } })
    })
    expect(name).toHaveProperty("value", "My local edit")

    mocks.workflow = containerWorkflow({
        build_status: "STARTED",
        name: "Renamed elsewhere",
        interval: 45,
        updated_at: "2026-01-06T00:00:00Z",
    })
    await poll()

    // Unsent edits survive the refresh, including fields the user did not
    // touch: saving must not silently drop them.
    expect(screen.getByLabelText("Name")).toHaveProperty("value", "My local edit")
    expect(screen.getByLabelText("Image")).toHaveProperty("value", "ubuntu:24.04")
    expect(screen.getByLabelText("Interval (minutes)")).toHaveProperty("value", "15")
})

it("submits the edited payload with an idempotency key and closes on success", async () => {
    mocks.workflow = heartbeatWorkflow()
    const user = userEvent.setup()
    const { onOpenChange } = renderDialog()
    expect(await screen.findByLabelText("Name")).toHaveProperty("value", "API heartbeat")

    await act(async () => {
        fireEvent.change(screen.getByLabelText("Name"), { target: { value: "API heartbeat v2" } })
    })
    await user.click(screen.getByLabelText("Endpoint URL"))
    await user.keyboard("?probe=1")
    await user.click(saveButton())

    await waitFor(() => expect(mocks.updateCalls).toHaveLength(1))
    const [call] = mocks.updateCalls
    expect(call.url).toBe("/workflows/w1")
    expect(call.init?.method).toBe("PUT")
    expect((call.init?.headers as Record<string, string>)["Idempotency-Key"]).toBe("idempotency-key")
    expect(JSON.parse(String(call.init?.body))).toEqual({
        name: "API heartbeat v2",
        payload: JSON.stringify({
            endpoint: "https://example.com/health?probe=1",
            expected_status_code: 204,
            headers: { "X-Token": "secret" },
            timeout: "10s",
        }),
        interval: 5,
        max_consecutive_job_failures_allowed: 4,
    })
    expect(onOpenChange).toHaveBeenCalledWith(false)
    expect(mocks.toast.success).toHaveBeenCalledWith("workflow updated successfully")
})

it("blocks editing and closing while the save is in flight", async () => {
    mocks.holdUpdates = true
    mocks.workflow = heartbeatWorkflow()
    const user = userEvent.setup()
    const { onOpenChange } = renderDialog()
    expect(await screen.findByLabelText("Name")).toHaveProperty("value", "API heartbeat")

    await user.click(saveButton())

    expect(await screen.findByRole("button", { name: /Saving/ })).toHaveProperty("disabled", true)
    expect(screen.getByRole("button", { name: "Cancel" })).toHaveProperty("disabled", true)
    expect(screen.getByRole("tab", { name: /Schedule/ })).toHaveProperty("disabled", true)

    // Neither the cancel button nor the escape key may abandon a save.
    await user.keyboard("{Escape}")
    expect(onOpenChange).not.toHaveBeenCalled()

    await act(async () => {
        mocks.releaseUpdate?.()
    })

    await waitFor(() => expect(onOpenChange).toHaveBeenCalledWith(false))
})

it("closes on escape once the editor is idle", async () => {
    const user = userEvent.setup()
    const { onOpenChange } = renderDialog()
    expect(await screen.findByLabelText("Name")).toHaveProperty("value", "Nightly container")

    await user.keyboard("{Escape}")

    expect(onOpenChange).toHaveBeenCalledWith(false)
    expect(mocks.updateCalls).toHaveLength(0)
})

it("keeps the dialog open and reports a rejected save", async () => {
    mocks.workflow = heartbeatWorkflow()
    mocks.fetchApi.mockImplementation(async () => {
        throw new Error("failed to update workflow: 409 conflict")
    })
    const user = userEvent.setup()
    const { onOpenChange } = renderDialog()
    expect(await screen.findByLabelText("Name")).toHaveProperty("value", "API heartbeat")

    await user.click(saveButton())

    await waitFor(() => expect(mocks.toast.error).toHaveBeenCalledWith("failed to update workflow: 409 conflict"))
    expect(onOpenChange).not.toHaveBeenCalled()
    expect(screen.getByRole("button", { name: /Save changes/ })).toHaveProperty("disabled", false)
})

it("keeps the dialog open and reports invalid edits on the section that failed", async () => {
    mocks.workflow = containerWorkflow()
    const user = userEvent.setup()
    const { onOpenChange } = renderDialog()
    expect(await screen.findByLabelText("Name")).toHaveProperty("value", "Nightly container")

    // An invalid configuration field keeps the dialog open and explains why.
    await act(async () => {
        fireEvent.change(screen.getByLabelText("Image"), { target: { value: "" } })
    })
    await user.click(saveButton())

    expect(await screen.findByText("Container image is required")).toBeTruthy()
    expect(mocks.updateCalls).toHaveLength(0)
    expect(onOpenChange).not.toHaveBeenCalled()
    expect(activeTab()).toBe("Configuration")

    // A schedule-only failure moves the view to the schedule section.
    await act(async () => {
        fireEvent.change(screen.getByLabelText("Image"), { target: { value: "alpine:latest" } })
    })
    await user.click(screen.getByRole("tab", { name: /Schedule/ }))
    await act(async () => {
        fireEvent.change(screen.getByLabelText("Interval (minutes)"), { target: { value: "0" } })
    })
    await user.click(saveButton())

    expect(await screen.findByText("Must be a whole number between 1 and 10080 minutes (1 week)")).toBeTruthy()
    expect(mocks.updateCalls).toHaveLength(0)
    expect(activeTab()).toBe("Schedule")
})

it("closes without saving when the editor is cancelled", async () => {
    const user = userEvent.setup()
    const { onOpenChange } = renderDialog()
    expect(await screen.findByLabelText("Name")).toHaveProperty("value", "Nightly container")

    await user.click(screen.getByRole("button", { name: "Cancel" }))

    expect(onOpenChange).toHaveBeenCalledWith(false)
    expect(mocks.updateCalls).toHaveLength(0)
})

it("renders nothing and fetches nothing while the dialog is closed", () => {
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(
        <QueryClientProvider client={queryClient}>
            <UpdateWorkflowDialog workflowId="w1" open={false} onOpenChange={vi.fn()} />
        </QueryClientProvider>,
    )
    expect(screen.queryByText("Update workflow")).toBeNull()
    expect(mocks.fetchApiJson).not.toHaveBeenCalled()
})

function installDomStubs() {
    if (!("ResizeObserver" in globalThis)) {
        class ResizeObserverStub {
            observe() {}
            unobserve() {}
            disconnect() {}
        }
        vi.stubGlobal("ResizeObserver", ResizeObserverStub)
    }
    if (!Element.prototype.hasPointerCapture) {
        Element.prototype.hasPointerCapture = () => false
        Element.prototype.setPointerCapture = () => {}
        Element.prototype.releasePointerCapture = () => {}
        Element.prototype.scrollIntoView = () => {}
    }
    if (!window.matchMedia) {
        vi.stubGlobal("matchMedia", (query: string) => ({
            matches: false,
            media: query,
            onchange: null,
            addEventListener: () => {},
            removeEventListener: () => {},
            addListener: () => {},
            removeListener: () => {},
            dispatchEvent: () => false,
        }))
    }
}

/** Puts the shared prototype back the way this file found it. */
function restoreDomStubs() {
    pointerCaptureMembers.forEach((member, index) => {
        const original = originalPointerCaptureDescriptors[index]
        if (original) {
            Object.defineProperty(Element.prototype, member, original)
        } else {
            Reflect.deleteProperty(Element.prototype, member)
        }
    })
}
