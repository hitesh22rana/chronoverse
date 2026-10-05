// @vitest-environment jsdom
/**
 * Mounted tests for the workflow details/jobs page: which actions each tab
 * offers, how lifecycle state decides between them, and how the jobs toolbar
 * moves filters, paging and refreshes through the URL.
 *
 * Only boundaries are mocked: the transport, the navigation hooks (with a
 * store-backed search string, so `router.push` navigates like the real router),
 * `sonner`, and the dialog surface Radix needs. Everything else runs for real.
 */
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import {
    act,
    cleanup,
    fireEvent,
    render,
    screen,
    waitFor,
    within,
} from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { useSyncExternalStore } from "react"
import { afterEach, beforeEach, expect, it, vi } from "vitest"

import type { Job } from "@/features/jobs/types"
import type { Workflow } from "./types"
import WorkflowDetailsAndJobsPage from "./workflow-details-page"

const mocks = vi.hoisted(() => {
    const searchStore = {
        value: "",
        listeners: new Set<() => void>(),
        get: () => searchStore.value,
        subscribe: (listener: () => void) => {
            searchStore.listeners.add(listener)
            return () => {
                searchStore.listeners.delete(listener)
            }
        },
        set: (next: string) => {
            searchStore.value = next
            searchStore.listeners.forEach((listener) => listener())
        },
    }

    return {
        searchStore,
        pathname: "/workflows/w1",
        push: vi.fn(),
        back: vi.fn(),
        replace: vi.fn(),
        fetchApi: vi.fn(),
        fetchApiJson: vi.fn(),
        toast: { error: vi.fn(), success: vi.fn(), warning: vi.fn() },
        workflow: null as unknown,
        jobsResponse: null as unknown,
        analytics: { workflow_id: "w1", total_jobs: 3, total_joblogs: 9, total_job_execution_duration: 125 },
        analyticsError: null as null | string,
        holdDetail: false,
        releaseDetail: null as null | (() => void),
        holdJobs: false,
        releaseJobs: null as null | (() => void),
        holdRequest: false,
        releaseRequest: null as null | (() => void),
    }
})

vi.mock("next/navigation", () => ({
    useParams: () => ({ workflowId: "w1" }),
    usePathname: () => mocks.pathname,
    useRouter: () => ({ push: mocks.push, back: mocks.back, replace: mocks.replace }),
    useSearchParams: () => {
        // Subscribing here is what makes `router.push` re-render the page, the
        // same way a real navigation hands the component new search params.
        useSyncExternalStore(mocks.searchStore.subscribe, mocks.searchStore.get, mocks.searchStore.get)
        return new URLSearchParams(mocks.searchStore.value)
    },
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
    payload: JSON.stringify({ image: "alpine:latest", cmd: ["sh", "-c", "echo hi"] }),
    build_status: "COMPLETED",
    interval: 15,
    consecutive_job_failures_count: 2,
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
    payload: JSON.stringify({ endpoint: "https://example.com/health" }),
    build_status: "COMPLETED",
    interval: 1440,
    max_consecutive_job_failures_allowed: 4,
    log_retention: false,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-02T00:00:00Z",
    ...overrides,
})

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

const jobsPage = { jobs: [job(), job({ id: "j2", status: "FAILED", trigger: "MANUAL" })], cursor: "cursor-2" }

function renderPage(search = "") {
    mocks.searchStore.set(search)
    const queryClient = new QueryClient({
        defaultOptions: { queries: { retry: false, gcTime: 0 }, mutations: { retry: false } },
    })
    const user = userEvent.setup()
    const view = render(
        <QueryClientProvider client={queryClient}>
            <WorkflowDetailsAndJobsPage />
        </QueryClientProvider>,
    )
    return { ...view, user }
}

/** The mocked transport records `[url, errorMessage, init]` per call. */
const recordedWrite = () => mocks.fetchApi.mock.calls.at(-1) as [string, string, RequestInit]
const requestedUrls = () => mocks.fetchApiJson.mock.calls.map((call) => String(call[0]))
/** An unfiltered job list request carries no "?", so match the path prefix. */
const jobListUrls = () => requestedUrls().filter((url: string) => url.startsWith("/workflows/w1/jobs"))
const lastPush = () => mocks.push.mock.calls.at(-1)?.[0] as string

/** Exact-name lookup would miss the active-filter badge inside the trigger. */
const filterButton = () => screen.getByRole("button", { name: /^Filters/ })
const failureBar = () => document.querySelector<HTMLElement>(".bg-orange-500")!
const findDialog = async () => within(await screen.findByRole("dialog"))
const refreshButton = () => screen.getByRole("button", { name: "Refresh" })
const nextPageButton = () => screen.getByRole("button", { name: "Next page" })
const previousPageButton = () => screen.getByRole("button", { name: "Previous page" })
const manualRunButton = () => screen.queryByRole("button", { name: /Manual run/ })

/** Parks a request until the test releases it, to hold a pending UI state. */
const blocker = (hold: () => boolean, release: (_resolve: () => void) => void) =>
    hold()
        ? new Promise<void>((resolve) => {
            release(resolve)
        })
        : Promise.resolve()

beforeEach(() => {
    mocks.pathname = "/workflows/w1"
    mocks.workflow = containerWorkflow()
    mocks.jobsResponse = jobsPage
    mocks.analyticsError = null
    mocks.holdDetail = false
    mocks.releaseDetail = null
    mocks.holdJobs = false
    mocks.releaseJobs = null
    mocks.holdRequest = false
    mocks.releaseRequest = null
    mocks.searchStore.set("")

    mocks.push.mockClear()
    mocks.back.mockClear()
    mocks.replace.mockClear()
    mocks.toast.error.mockClear()
    mocks.toast.success.mockClear()
    mocks.toast.warning.mockClear()

    mocks.push.mockImplementation((url: string) => {
        mocks.searchStore.set(url.replace(/^[?]/, ""))
    })
    mocks.back.mockImplementation(() => {
        mocks.searchStore.set("")
    })
    mocks.fetchApi.mockReset().mockImplementation(async (url: string) => {
        await blocker(() => mocks.holdRequest, (resolve) => {
            mocks.releaseRequest = resolve
        })
        return { ok: true, url }
    })
    mocks.fetchApiJson.mockReset().mockImplementation(async (url: string) => {
        if (url.startsWith("/workflows/w1/jobs")) {
            await blocker(() => mocks.holdJobs, (resolve) => {
                mocks.releaseJobs = resolve
            })
            if (mocks.jobsResponse instanceof Error) throw mocks.jobsResponse
            if (url.includes("status=") || url.includes("trigger=")) return { jobs: [] }
            return mocks.jobsResponse
        }
        if (url.startsWith("/workflows/w1") && !url.includes("/jobs")) {
            await blocker(() => mocks.holdDetail, (resolve) => {
                mocks.releaseDetail = resolve
            })
            if (mocks.workflow instanceof Error) throw mocks.workflow
            return mocks.workflow
        }
        if (url.startsWith("/analytics/w1")) {
            if (mocks.analyticsError) throw new Error(mocks.analyticsError)
            return mocks.analytics
        }
        throw new Error(`unexpected request: ${url}`)
    })
    installDomStubs()
})

afterEach(() => {
    cleanup()
    vi.unstubAllGlobals()
    restoreDomStubs()
})

// Details tab.

it("summarizes a container workflow, its configuration and its lifetime signals", async () => {
    renderPage()

    expect(await screen.findByRole("heading", { name: "Nightly container" })).toBeTruthy()
    expect(within(screen.getByText("Workflow kind").parentElement!).getByText("CONTAINER")).toBeTruthy()
    expect(screen.getByText("every 15 minutes")).toBeTruthy()
    expect(within(screen.getByText("Status").parentElement!).getByText("Active")).toBeTruthy()
    expect(screen.getByText("Enabled")).toBeTruthy()
    expect(screen.getByText(/"image": "alpine:latest"/)).toBeTruthy()
    expect(screen.getByText("2 / 3")).toBeTruthy()
    expect(failureBar().style.width).toBe(`${(2 / 3) * 100}%`)

    // 9 logs over 3 recorded job executions.
    expect(screen.getByText("~3 logs per job")).toBeTruthy()
    expect(screen.getByText("2m 5s")).toBeTruthy()
    expect(screen.getByText(/^Last updated/)).toBeTruthy()

    // The details tab never asks for the job list.
    expect(jobListUrls()).toHaveLength(0)
})

it("shows the heartbeat variant of the same summary", async () => {
    mocks.workflow = heartbeatWorkflow({ consecutive_job_failures_count: 4 })
    renderPage()

    expect(await screen.findByRole("heading", { name: "API heartbeat" })).toBeTruthy()
    expect(document.querySelector(".lucide-heart-pulse")).toBeTruthy()
    expect(screen.getByText("daily")).toBeTruthy()
    expect(screen.getByText("Disabled")).toBeTruthy()
    expect(screen.getByText(/https:\/\/example.com\/health/)).toBeTruthy()
    expect(screen.getByText("4 / 4")).toBeTruthy()
    expect(failureBar().style.width).toBe("100%")

    // Heartbeats emit no logs, so the metric explains itself instead of
    // promising a per-job rate.
    expect(screen.getByText("Not emitted by this workflow kind")).toBeTruthy()
    expect(screen.queryByText("~3 logs per job")).toBeNull()
    expect(screen.getByText("This workflow kind does not retain execution logs.")).toBeTruthy()
})

it("counts a workflow that has not failed a job yet", async () => {
    // The API omits a zero failure count, so a healthy workflow has no such field.
    mocks.workflow = containerWorkflow({ consecutive_job_failures_count: undefined })
    renderPage()

    expect(await screen.findByRole("heading", { name: "Nightly container" })).toBeTruthy()
    expect(screen.getByText("0 / 3")).toBeTruthy()
    expect(failureBar().style.width).toBe("0%")
})

it("says so when a workflow carries no configuration", async () => {
    mocks.workflow = containerWorkflow({ payload: undefined })
    renderPage()

    expect(await screen.findByRole("heading", { name: "Nightly container" })).toBeTruthy()
    expect(screen.getByText("No configuration available")).toBeTruthy()
})

it("keeps analytics failures recoverable from the details card", async () => {
    mocks.analyticsError = "failed to fetch workflow analytics: offline"
    renderPage()

    expect(await screen.findByText("Analytics unavailable")).toBeTruthy()
    expect(screen.getByText("failed to fetch workflow analytics: offline")).toBeTruthy()
    expect(screen.queryByText("Job executions")).toBeNull()

    mocks.analyticsError = null
    await act(async () => {
        fireEvent.click(screen.getByRole("button", { name: /Try again/ }))
    })

    expect(await screen.findByText("Job executions")).toBeTruthy()
})

it("holds the details card back while loading but still offers editing", async () => {
    mocks.holdDetail = true
    renderPage()

    // Nothing is known yet: no name, no status, and the destructive slot is a
    // placeholder because its label depends on the lifecycle state.
    await waitFor(() => expect(screen.getByRole("button", { name: /Edit workflow/ })).toBeTruthy())
    expect(screen.queryByRole("heading", { level: 1 })).toBeNull()
    expect(screen.queryByRole("button", { name: /Terminate workflow/ })).toBeNull()
    expect(screen.queryByRole("button", { name: /Delete workflow/ })).toBeNull()
    expect(screen.queryByText("Failure tracking")).toBeNull()
    expect(document.querySelectorAll("[data-slot=skeleton]").length).toBeGreaterThan(0)

    await act(async () => {
        mocks.releaseDetail?.()
    })

    expect(await screen.findByText("Failure tracking")).toBeTruthy()
    expect(await screen.findByRole("button", { name: /Terminate workflow/ })).toBeTruthy()
})

it("reports a failed workflow load without offering a lifecycle action", async () => {
    mocks.workflow = new Error("failed to fetch workflow details: offline") as never
    renderPage()

    expect(await screen.findByText("Error loading workflow details")).toBeTruthy()
    expect(mocks.toast.error).toHaveBeenCalledWith("failed to fetch workflow details: offline")
    expect(screen.queryByText("Failure tracking")).toBeNull()
    expect(screen.getByRole("button", { name: /Edit workflow/ })).toBeTruthy()
    // A failed load leaves the lifecycle unknown and the lifecycle dialogs
    // unmounted, so the strip must not offer terminate or delete.
    expect(screen.queryByRole("button", { name: /Terminate workflow/ })).toBeNull()
    expect(screen.queryByRole("button", { name: /Delete workflow/ })).toBeNull()
})

it("swaps terminate for delete once the workflow is terminated", async () => {
    mocks.workflow = containerWorkflow({ terminated_at: "2026-01-03T00:00:00Z" })
    renderPage()

    expect(await screen.findByRole("button", { name: /Delete workflow/ })).toBeTruthy()
    expect(screen.queryByRole("button", { name: /Terminate workflow/ })).toBeNull()
    // The header badge reports the lifecycle, not the stale build status.
    expect(within(screen.getByText("Status").parentElement!).getByText("Terminated")).toBeTruthy()
})

it("opens the editor for the loaded workflow and closes it again", async () => {
    const { user } = renderPage()
    expect(await screen.findByRole("heading", { name: "Nightly container" })).toBeTruthy()

    await user.click(screen.getByRole("button", { name: /Edit workflow/ }))

    expect(await screen.findByText("Update workflow")).toBeTruthy()
    expect(screen.getByLabelText("Name")).toHaveProperty("value", "Nightly container")

    await user.click(screen.getByRole("button", { name: "Cancel" }))
    await waitFor(() => expect(screen.queryByLabelText("Name")).toBeNull())
})

it("terminates a running workflow only after its name is confirmed", async () => {
    const { user } = renderPage()
    expect(await screen.findByRole("button", { name: /Terminate workflow/ })).toBeTruthy()

    await user.click(screen.getByRole("button", { name: /Terminate workflow/ }))

    const dialog = await findDialog()
    expect(dialog.getByRole("heading", { name: "Terminate workflow" })).toBeTruthy()
    expect(
        dialog.getByText("Terminating this workflow will terminate all ongoing jobs and prevent any future scheduled executions."),
    ).toBeTruthy()
    expect(dialog.getByRole("button", { name: "Terminate workflow" })).toHaveProperty("disabled", true)

    await user.type(screen.getByPlaceholderText("Enter workflow name"), "Nightly")
    expect(dialog.getByRole("button", { name: "Terminate workflow" })).toHaveProperty("disabled", true)
    await user.type(screen.getByPlaceholderText("Enter workflow name"), " container")

    await user.click(dialog.getByRole("button", { name: "Terminate workflow" }))

    await waitFor(() => expect(mocks.fetchApi).toHaveBeenCalledTimes(1))
    const [url, , init] = recordedWrite()
    expect(url).toBe("/workflows/w1")
    expect(init.method).toBe("PATCH")
    expect(mocks.toast.success).toHaveBeenCalledWith("workflow terminated successfully")
    // Terminating stays on the page; only deleting navigates away.
    expect(mocks.push).not.toHaveBeenCalled()
})

it("deletes a terminated workflow and returns to the workflow list", async () => {
    mocks.workflow = containerWorkflow({ terminated_at: "2026-01-03T00:00:00Z" })
    const { user } = renderPage()
    expect(await screen.findByRole("button", { name: /Delete workflow/ })).toBeTruthy()

    await user.click(screen.getByRole("button", { name: /Delete workflow/ }))

    const dialog = await findDialog()
    expect(dialog.getByRole("heading", { name: "Delete workflow" })).toBeTruthy()
    expect(dialog.getByText(/This action cannot be undone, and will delete the workflow/)).toBeTruthy()
    await user.type(screen.getByPlaceholderText("Enter workflow name"), "Nightly container")
    await user.click(dialog.getByRole("button", { name: "Delete workflow" }))

    await waitFor(() => expect(mocks.fetchApi).toHaveBeenCalledTimes(1))
    const [url, , init] = recordedWrite()
    expect(url).toBe("/workflows/w1")
    expect(init.method).toBe("DELETE")
    expect(mocks.toast.success).toHaveBeenCalledWith("workflow deleted successfully")
    expect(mocks.push).toHaveBeenCalledWith("/")
})

it("moves between the details and jobs tabs through the address", async () => {
    const { user } = renderPage()
    expect(await screen.findByRole("heading", { name: "Nightly container" })).toBeTruthy()

    await user.click(screen.getByRole("tab", { name: /Jobs/ }))

    await waitFor(() => expect(lastPush()).toBe("?tab=jobs"))
    expect((await screen.findByRole("tab", { selected: true })).textContent).toBe("Jobs")
    expect(await screen.findByText(/Job: j1/)).toBeTruthy()
    expect(screen.queryByRole("button", { name: /Edit workflow/ })).toBeNull()
    // Analytics belong to the details tab only.
    expect(requestedUrls().filter((url: string) => url.startsWith("/analytics"))).toHaveLength(1)

    await user.click(filterButton())
    await screen.findByText("Filter by")
    await user.click(screen.getAllByRole("combobox")[1])
    await user.click(await screen.findByRole("option", { name: "Failed" }))
    await user.click(screen.getByRole("button", { name: "Apply Filters" }))
    await waitFor(() => expect(lastPush()).toBe("?tab=jobs&status=FAILED"))

    await user.click(screen.getByRole("tab", { name: /Details/ }))

    // Returning to details drops the tab and every jobs-only filter.
    await waitFor(() => expect(lastPush()).toBe("?"))
    expect(await screen.findByRole("button", { name: /Edit workflow/ })).toBeTruthy()
    expect(screen.queryByRole("button", { name: /^Filters/ })).toBeNull()
})

// Jobs tab.

it("lists jobs with the paging the cursor supports", async () => {
    renderPage("tab=jobs")

    expect(await screen.findByText(/Job: j1/)).toBeTruthy()
    expect(screen.getByText(/Job: j2/)).toBeTruthy()
    expect(screen.getByRole("link", { name: /Job: j1/ }).getAttribute("href")).toBe("/workflows/w1/jobs/j1")
    // The response advertises a next page but the URL has no cursor yet.
    expect(nextPageButton()).toHaveProperty("disabled", false)
    expect(previousPageButton()).toHaveProperty("disabled", true)
    expect(screen.queryByText("No jobs found")).toBeNull()
})

it.each([
    ["CONTAINER", "COMPLETED", undefined, true],
    ["HEARTBEAT", "COMPLETED", undefined, true],
    ["CONTAINER", "QUEUED", undefined, false],
    ["CONTAINER", "STARTED", undefined, false],
    ["CONTAINER", "FAILED", undefined, false],
    ["CONTAINER", "COMPLETED", "2026-01-03T00:00:00Z", false],
] as const)(
    "offers a manual run for a %s workflow built %s only when it is still live",
    async (kind, buildStatus, terminatedAt, expected) => {
        mocks.workflow = kind === "HEARTBEAT"
            ? heartbeatWorkflow({ build_status: buildStatus, terminated_at: terminatedAt })
            : containerWorkflow({ build_status: buildStatus, terminated_at: terminatedAt })
        renderPage("tab=jobs")

        await screen.findByText(/Job: j1/)
        const button = manualRunButton()
        if (expected) {
            expect(button).toBeTruthy()
        } else {
            expect(button).toBeNull()
        }
    },
)

it("schedules a manual run with an idempotency key and blocks a second click", async () => {
    mocks.holdRequest = true
    const { user } = renderPage("tab=jobs")
    const button = await screen.findByRole("button", { name: /Manual run/ })

    await user.click(button)

    await waitFor(() => expect(manualRunButton()).toHaveProperty("disabled", true))
    await waitFor(() => expect(mocks.fetchApi).toHaveBeenCalledTimes(1))
    const [url, , init] = recordedWrite()
    expect(url).toBe("/workflows/w1/jobs/schedule")
    expect(init.method).toBe("POST")
    expect((init.headers as Record<string, string>)["Idempotency-Key"]).toBe("idempotency-key")

    await act(async () => {
        mocks.releaseRequest?.()
    })

    await waitFor(() => expect(manualRunButton()).toHaveProperty("disabled", false))
    expect(mocks.toast.success).toHaveBeenCalledWith("Job scheduled successfully")
    // A scheduled run refreshes the list it landed in.
    await waitFor(() => expect(jobListUrls().length).toBeGreaterThan(1))
})

it("keeps the manual run usable after a rejected schedule", async () => {
    mocks.fetchApi.mockRejectedValue(new Error("Failed to schedule job: 409 conflict"))
    const { user } = renderPage("tab=jobs")
    const button = await screen.findByRole("button", { name: /Manual run/ })

    await user.click(button)

    await waitFor(() => expect(mocks.toast.error).toHaveBeenCalledWith("Failed to schedule job: 409 conflict"))
    expect(manualRunButton()).toHaveProperty("disabled", false)
})

it("keeps a pending filter out of the request until Apply", async () => {
    const { user } = renderPage("tab=jobs")
    await screen.findByText(/Job: j1/)

    await user.click(filterButton())
    expect(await screen.findByText("Filter by")).toBeTruthy()
    // Nothing is applied yet, so there is nothing to clear and no badge.
    expect(screen.queryByRole("button", { name: /Clear all/ })).toBeNull()
    expect(within(filterButton()).queryByText("1")).toBeNull()

    await user.click(screen.getAllByRole("combobox")[0])
    await user.click(await screen.findByRole("option", { name: "Manual" }))
    await user.click(screen.getAllByRole("combobox")[1])
    await user.click(await screen.findByRole("option", { name: "Failed" }))

    // Picking values changes nothing about the request the page has issued.
    expect(mocks.push).not.toHaveBeenCalled()
    expect(jobListUrls().at(-1)).toBe("/workflows/w1/jobs")

    await user.click(screen.getByRole("button", { name: "Apply Filters" }))

    await waitFor(() => expect(lastPush()).toBe("?tab=jobs&status=FAILED&trigger=MANUAL"))
    expect(await screen.findByText("No jobs found")).toBeTruthy()
    await waitFor(() => expect(jobListUrls().at(-1)).toBe("/workflows/w1/jobs?status=FAILED&trigger=MANUAL"))
    expect(within(filterButton()).getByText("2")).toBeTruthy()
})

it("reseeds the filter popover from the applied filters and can drop them one by one", async () => {
    const { user } = renderPage("tab=jobs&status=FAILED&trigger=MANUAL")
    await waitFor(() =>
        expect(jobListUrls()).toEqual(["/workflows/w1/jobs?status=FAILED&trigger=MANUAL"]),
    )
    expect(within(filterButton()).getByText("2")).toBeTruthy()

    await user.click(filterButton())
    expect(await screen.findByText("Manual")).toBeTruthy()
    expect(screen.getAllByRole("combobox")[0].textContent).toBe("Manual")
    expect(screen.getAllByRole("combobox")[1].textContent).toBe("Failed")

    // Reopening after abandoning an edit must not resurrect the abandoned pick.
    await user.click(screen.getAllByRole("combobox")[0])
    await user.click(await screen.findByRole("option", { name: "Automatic" }))
    await user.keyboard("{Escape}")

    await user.click(filterButton())
    expect(screen.getAllByRole("combobox")[0].textContent).toBe("Manual")

    await user.click(screen.getAllByRole("combobox")[1])
    await user.click(await screen.findByRole("option", { name: "All statuses" }))
    await user.click(screen.getByRole("button", { name: "Apply Filters" }))

    await waitFor(() => expect(lastPush()).toBe("?tab=jobs&trigger=MANUAL"))
    expect(within(filterButton()).getByText("1")).toBeTruthy()

    await user.click(filterButton())
    await user.click(screen.getAllByRole("combobox")[0])
    await user.click(await screen.findByRole("option", { name: "All triggers" }))
    await user.click(screen.getByRole("button", { name: "Apply Filters" }))

    await waitFor(() => expect(lastPush()).toBe("?tab=jobs"))
    expect(within(filterButton()).queryByText("1")).toBeNull()
})

it("clears every applied filter but keeps the open tab", async () => {
    const { user } = renderPage("tab=jobs&status=FAILED&trigger=MANUAL")
    await waitFor(() =>
        expect(jobListUrls()).toEqual(["/workflows/w1/jobs?status=FAILED&trigger=MANUAL"]),
    )

    await user.click(filterButton())
    await user.click(await screen.findByRole("button", { name: /Clear all/ }))

    await waitFor(() => expect(lastPush()).toBe("?tab=jobs"))
    await waitFor(() => expect(jobListUrls().at(-1)).toBe("/workflows/w1/jobs"))
    expect(within(filterButton()).queryByText("1")).toBeNull()
})

it("disables refresh while the job list is in flight and refetches on demand", async () => {
    mocks.holdJobs = true
    const { user } = renderPage("tab=jobs")

    await waitFor(() => expect(refreshButton()).toHaveProperty("disabled", true))
    expect(refreshButton().querySelector("svg")?.getAttribute("class")).toContain("animate-spin")

    await act(async () => {
        mocks.releaseJobs?.()
    })

    await waitFor(() => expect(screen.findByText(/Job: j1/)).resolves.toBeTruthy())
    await waitFor(() => expect(refreshButton()).toHaveProperty("disabled", false))
    const before = jobListUrls().length

    await user.click(refreshButton())

    await waitFor(() => expect(jobListUrls().length).toBe(before + 1))
    // Refreshing the jobs tab must not reach for workflow analytics.
    expect(requestedUrls().filter((url: string) => url.startsWith("/analytics"))).toHaveLength(0)
})

it("walks the job cursor forward and back through history", async () => {
    const { user } = renderPage("tab=jobs")
    await screen.findByText(/Job: j1/)

    await user.click(nextPageButton())

    await waitFor(() => expect(lastPush()).toBe("?tab=jobs&cursor=cursor-2"))
    await waitFor(() => expect(jobListUrls().at(-1)).toBe("/workflows/w1/jobs?cursor=cursor-2"))
    expect(previousPageButton()).toHaveProperty("disabled", false)
    expect(nextPageButton()).toHaveProperty("disabled", false)

    await user.click(previousPageButton())

    expect(mocks.back).toHaveBeenCalledTimes(1)
})

it("tells a filtered list apart from a workflow that never ran", async () => {
    mocks.jobsResponse = { jobs: [] }
    renderPage("tab=jobs&status=FAILED")

    expect(await screen.findByText("No jobs found")).toBeTruthy()
    expect(screen.getByText("Try adjusting your search query or filters.")).toBeTruthy()
    expect(within(filterButton()).getByText("1")).toBeTruthy()

    cleanup()
    mocks.jobsResponse = { jobs: [] }
    renderPage("tab=jobs")

    expect(await screen.findByText("No jobs found")).toBeTruthy()
    expect(screen.getByText("This workflow hasn't run any jobs yet.")).toBeTruthy()
    expect(refreshButton()).toBeTruthy()
})

it("surfaces a failed job list load without losing the toolbar", async () => {
    mocks.jobsResponse = new Error("Failed to fetch workflow jobs: offline") as never
    renderPage("tab=jobs")

    expect(await screen.findByText("Error loading jobs")).toBeTruthy()
    expect(mocks.toast.error).toHaveBeenCalledWith("Failed to fetch workflow jobs: offline")
    expect(refreshButton()).toBeTruthy()
    expect(filterButton()).toBeTruthy()
    expect(manualRunButton()).toBeTruthy()
})

it("rejects an unknown tab instead of showing an empty panel", async () => {
    renderPage("tab=history")

    expect(await screen.findByText("Unknown tab")).toBeTruthy()
    expect(screen.queryByRole("button", { name: /Filters/ })).toBeNull()
    expect(screen.queryByRole("button", { name: /Edit workflow/ })).toBeNull()
    // Only the workflow itself is loaded for a tab the page cannot render.
    expect(requestedUrls()).toEqual(["/workflows/w1"])
})

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
