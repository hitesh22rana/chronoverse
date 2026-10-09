// @vitest-environment jsdom
/**
 * Mounted analytics tests exercise the real drawer, query states and controls;
 * only transport and toast boundaries are mocked. jsdom measures no box, so the
 * charts draw nothing here; chart drawing is covered by
 * user-analytics-overview.test.tsx, which measures the container instead.
 */
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { afterEach, beforeEach, expect, it, vi } from "vitest"

import { UserAnalyticsDrawer } from "@/features/analytics/user-analytics-drawer"

const mocks = vi.hoisted(() => ({
    fetchApiJson: vi.fn(),
    payload: null as unknown,
    error: null as null | Error,
    holdFetch: false,
    releaseFetch: null as null | (() => void),
}))

vi.mock("sonner", () => ({ toast: { error: vi.fn(), success: vi.fn(), warning: vi.fn() }, Toaster: () => null }))

vi.mock("@/lib/api/client", () => ({
    fetchApiJson: mocks.fetchApiJson,
    fetchApi: vi.fn(),
    createIdempotencyKey: () => "idempotency-key",
}))

const populated = {
    total_workflows: 4,
    total_jobs: 12,
    total_joblogs: 30,
    total_job_execution_duration: 3600,
    workflow_kinds: [
        {
            kind: "CONTAINER",
            total_workflows: 2,
            total_jobs: 8,
            total_joblogs: 20,
            total_job_execution_duration: 2400,
        },
    ],
    top_workflows: [
        {
            workflow_id: "w1",
            workflow_name: "Nightly container",
            kind: "CONTAINER",
            total_jobs: 8,
            total_joblogs: 20,
            total_job_execution_duration: 2400,
        },
    ],
}

const empty = {
    total_workflows: 0,
    total_jobs: 0,
    total_joblogs: 0,
    total_job_execution_duration: 0,
    workflow_kinds: [],
    top_workflows: [],
}

function renderDrawer() {
    const queryClient = new QueryClient({
        defaultOptions: { queries: { retry: false, gcTime: 0 }, mutations: { retry: false } },
    })
    const user = userEvent.setup()
    const view = render(
        <QueryClientProvider client={queryClient}>
            <UserAnalyticsDrawer />
        </QueryClientProvider>,
    )
    return { ...view, user, queryClient }
}

const trigger = () => screen.getByRole("button", { name: /Analytics/ })
const refreshButton = () => screen.getByRole("button", { name: "Refresh analytics" })
const analyticsRequests = () => mocks.fetchApiJson.mock.calls.length

/** The headline card for one metric, with its stated total and derived rate. */
function metric(label: string) {
    const card = screen.getByText(label).closest("[data-slot=card]")?.querySelector("[data-slot=card-content]")
    if (!card) throw new Error(`no metric card for ${label}`)
    const [value, helper] = card.querySelectorAll("p")
    return { value: value?.textContent, helper: helper?.textContent }
}

/** Opens the drawer and waits for its first fetch to settle. */
async function openDrawer(user: ReturnType<typeof userEvent.setup>) {
    await user.click(trigger())
    return screen.findByText("Analytics overview")
}

/** DOM methods missing from jsdom. */
const pointerCaptureMembers = [
    "hasPointerCapture",
    "setPointerCapture",
    "releasePointerCapture",
    "scrollIntoView",
    "scrollTo",
] as const

/** Restore the original DOM methods after each test. */
const originalPointerCaptureDescriptors = pointerCaptureMembers.map((member) =>
    Object.getOwnPropertyDescriptor(Element.prototype, member)
)

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
    if (!Element.prototype.scrollTo) {
        Element.prototype.scrollTo = () => {}
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

beforeEach(() => {
    mocks.payload = populated
    mocks.error = null
    mocks.holdFetch = false
    mocks.releaseFetch = null
    mocks.fetchApiJson.mockReset().mockImplementation(async () => {
        if (mocks.holdFetch) {
            await new Promise<void>((resolve) => {
                mocks.releaseFetch = resolve
            })
        }
        if (mocks.error) throw mocks.error
        return mocks.payload
    })
    installDomStubs()
})

afterEach(() => {
    cleanup()
    vi.unstubAllGlobals()
    restoreDomStubs()
})

it("does not request analytics until the drawer is opened", async () => {
    const { user } = renderDrawer()

    expect(analyticsRequests()).toBe(0)

    await openDrawer(user)

    await waitFor(() => expect(analyticsRequests()).toBe(1))
    expect(mocks.fetchApiJson.mock.calls[0][0]).toBe("/analytics")
})

it("reports the account totals once the drawer has them", async () => {
    const { user } = renderDrawer()

    await openDrawer(user)

    expect(await screen.findByText("Workload mix")).toBeTruthy()
    expect(screen.getByText("Most active workflows")).toBeTruthy()
    // The four headline totals come straight from the response.
    expect(metric("Workflows")).toEqual({ value: "4", helper: "~3 jobs per workflow" })
    expect(metric("Terminal jobs")).toEqual({ value: "12", helper: "5m average runtime" })
    expect(metric("Generated logs")).toEqual({ value: "30", helper: "~3 logs per job" })
    expect(metric("Execution time")).toEqual({ value: "1h", helper: "Across 12 terminal jobs" })
    // The single workflow kind in the payload is listed next to its job count.
    expect(screen.getByText("Container")).toBeTruthy()
})

it("shows a placeholder while the first analytics load is in flight", async () => {
    mocks.holdFetch = true
    const { user } = renderDrawer()

    await openDrawer(user)

    // The headline is there but the numbers are not invented yet.
    expect(refreshButton()).toHaveProperty("disabled", true)
    expect(screen.queryByText("Workload mix")).toBeNull()

    await act(async () => {
        mocks.releaseFetch?.()
    })

    expect(await screen.findByText("Workload mix")).toBeTruthy()
    await waitFor(() => expect(refreshButton()).toHaveProperty("disabled", false))
})

it("refetches on demand and cannot stack a second request behind one", async () => {
    const { user } = renderDrawer()
    await openDrawer(user)
    await screen.findByText("Workload mix")
    await waitFor(() => expect(refreshButton()).toHaveProperty("disabled", false))
    const before = analyticsRequests()

    mocks.holdFetch = true
    await user.click(refreshButton())

    await waitFor(() => expect(refreshButton()).toHaveProperty("disabled", true))
    expect(refreshButton().querySelector("svg")?.getAttribute("class")).toContain("animate-spin")
    expect(analyticsRequests()).toBe(before + 1)

    // The control is disabled, so a second request cannot stack behind this one.
    await user.click(refreshButton())
    expect(refreshButton()).toHaveProperty("disabled", true)
    expect(analyticsRequests()).toBe(before + 1)

    await act(async () => {
        mocks.releaseFetch?.()
    })

    await waitFor(() => expect(refreshButton()).toHaveProperty("disabled", false))
})

it("recovers from a failed analytics load without a reload", async () => {
    mocks.error = new Error("failed to fetch analytics: offline")
    const { user } = renderDrawer()

    await openDrawer(user)

    expect(await screen.findByText("Analytics unavailable")).toBeTruthy()
    expect(screen.getByText("failed to fetch analytics: offline")).toBeTruthy()
    // A failure has to be retryable from the drawer itself.
    expect(screen.queryByText("Workload mix")).toBeNull()

    mocks.error = null
    await act(async () => {
        fireEvent.click(screen.getByRole("button", { name: "Try again" }))
    })

    expect(await screen.findByText("Workload mix")).toBeTruthy()
    expect(screen.queryByText("Analytics unavailable")).toBeNull()
})

it("explains an empty account instead of drawing empty charts", async () => {
    mocks.payload = empty
    const { user } = renderDrawer()

    await openDrawer(user)

    expect(await screen.findByText("No terminal job activity yet")).toBeTruthy()
    expect(screen.getByText("Run a workflow to see terminal jobs grouped by workflow kind.")).toBeTruthy()
    expect(screen.getByText("No workflow activity yet")).toBeTruthy()
    // With no jobs there is nothing to average, and the card says so.
    expect(screen.getByText("No terminal jobs recorded")).toBeTruthy()
    expect(screen.getByText("No logs generated")).toBeTruthy()
    // The section titles stay put so the drawer keeps its shape.
    expect(screen.getByText("Workload mix")).toBeTruthy()
    expect(screen.getByText("Most active workflows")).toBeTruthy()
})
