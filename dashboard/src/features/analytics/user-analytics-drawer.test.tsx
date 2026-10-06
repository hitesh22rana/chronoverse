// @vitest-environment jsdom
/**
 * Mounted tests for the user analytics drawer: that it stays unfetched until it
 * is opened, what it reports for a real account, what an account with no
 * activity looks like, how a failed load recovers, and what the refresh control
 * does while a request is in flight.
 *
 * Boundaries only: the transport (`@/lib/api/client`) and `sonner`. React Query,
 * the Radix drawer and recharts all run for real, so the assertions are about
 * the totals and states a user can read.
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

/** Opens the drawer and waits for its first fetch to settle. */
async function openDrawer(user: ReturnType<typeof userEvent.setup>) {
    await user.click(trigger())
    return screen.findByText("Analytics overview")
}

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
    expect(screen.getByText("Workflows")).toBeTruthy()
    expect(screen.getByText("4")).toBeTruthy()
    expect(screen.getByText("12")).toBeTruthy()
    expect(screen.getByText("30")).toBeTruthy()
    expect(screen.getByText("1h")).toBeTruthy()
    // Derived per-workflow and per-job figures are stated rather than implied.
    expect(screen.getByText("~3 jobs per workflow")).toBeTruthy()
    expect(screen.getByText("~3 logs per job")).toBeTruthy()
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
