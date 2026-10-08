// @vitest-environment jsdom
/**
 * Mounted tests for the job details page timeline: how an elapsed job run is
 * reported, at every boundary the duration formatter branches on.
 *
 * The boundaries mocked are the transport, the Next.js navigation hooks and
 * `sonner`. `JobDetailsAndLogsPage`, `useJobDetails`, `JobTimeline`,
 * `formatDuration` and react-query all run for real against a valid job
 * document, so each expectation is the text a user reads for that run.
 */
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react"
import { afterEach, beforeEach, expect, it, vi } from "vitest"

import type { Job } from "@/features/jobs/types"
import { queryRefetchIntervals } from "@/lib/api/query-policy"
import JobDetailsAndLogsPage from "./job-details-page"

const mocks = vi.hoisted(() => ({
    job: null as unknown as Job,
    push: vi.fn(),
    replace: vi.fn(),
    back: vi.fn(),
    fetchApi: vi.fn(),
    fetchApiJson: vi.fn(),
    toast: { error: vi.fn(), success: vi.fn(), warning: vi.fn() },
}))

vi.mock("next/navigation", () => ({
    useParams: () => ({ workflowId: "w1", jobId: "j1" }),
    usePathname: () => "/workflows/w1/jobs/j1",
    useSearchParams: () => new URLSearchParams(),
    useRouter: () => ({ push: mocks.push, replace: mocks.replace, back: mocks.back }),
}))

vi.mock("sonner", () => ({ toast: mocks.toast, Toaster: () => null }))

vi.mock("@/lib/api/client", () => ({
    fetchApi: mocks.fetchApi,
    fetchApiJson: mocks.fetchApiJson,
    createIdempotencyKey: () => "idempotency-key",
}))

const workflow = {
    id: "w1",
    name: "Nightly container",
    kind: "CONTAINER",
    payload: JSON.stringify({ image: "alpine:latest" }),
    build_status: "COMPLETED",
    interval: 15,
    max_consecutive_job_failures_allowed: 3,
    log_retention: true,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
}

const startedAt = "2026-01-01T00:00:00Z"

/** A finished job that ran for `seconds`, which is what the page formats. */
const finishedJob = (seconds: number): Job => ({
    id: "j1",
    workflow_id: "w1",
    status: "COMPLETED",
    trigger: "AUTOMATIC",
    scheduled_at: startedAt,
    started_at: startedAt,
    completed_at: new Date(Date.parse(startedAt) + seconds * 1000).toISOString(),
    created_at: startedAt,
    updated_at: new Date(Date.parse(startedAt) + seconds * 1000).toISOString(),
})

function renderPage() {
    const queryClient = new QueryClient({
        defaultOptions: { queries: { retry: false, gcTime: 0 }, mutations: { retry: false } },
    })

    return render(
        <QueryClientProvider client={queryClient}>
            <JobDetailsAndLogsPage />
        </QueryClientProvider>,
    )
}

/**
 * `textContent` of a node with its whitespace collapsed, so reformatting the JSX
 * around a label is not a test failure. `getByText` throws when the node is gone,
 * so a missing row fails here instead of comparing against `undefined`.
 */
const collapsedText = (node: HTMLElement, label: string) => {
    const text = node.textContent?.replace(/\s+/g, " ").trim() ?? ""
    return text.startsWith(label) ? text.slice(label.length) : text
}

/** The duration cell of the timeline, which is the only row reading a duration. */
const renderedDuration = () => collapsedText(screen.getByText("Duration:").parentElement as HTMLElement, "Duration:")

const timelineRow = (label: string) =>
    collapsedText(screen.getByText(`${label}:`).parentElement as HTMLElement, `${label}:`)

/**
 * Requests for the job document itself. The path is anchored at the end so the
 * log pages under the same prefix are not counted as a refresh of the job, and so
 * a configured API base does not hide it.
 */
const jobDetailRequests = () =>
    mocks.fetchApiJson.mock.calls.filter(([url]) => isJobDetail(url)).length

/**
 * `apiEndpoints` prefixes every route with `NEXT_PUBLIC_API_URL`, so these stubs
 * match on the tail of the path rather than on a relative prefix. Matching the
 * head would make every test in this file fail for a deployment that configures
 * an API base, which is a normal way to run the dashboard.
 */
const isJobDetail = (url: unknown) => String(url).endsWith("/workflows/w1/jobs/j1")

/** Serves `job` for the first `failures` job-detail requests, then recovers. */
const failJobDetailTimes = (failures: number, error: unknown) => {
    mocks.fetchApiJson.mockImplementation(async (url: string) => {
        if (url.includes("/logs")) return { id: "j1", workflow_id: "w1", logs: [] }
        if (isJobDetail(url)) {
            if (failures > 0) {
                failures -= 1
                throw error
            }
            return mocks.job
        }
        if (url.includes("/workflows/w1")) return workflow
        throw new Error(`unexpected transport call: ${url}`)
    })
}

/**
 * The timeline prints timestamps in the reader's own timezone, so the expected
 * calendar day is derived the same way. A fixed literal would only hold for
 * runners east of UTC, and this suite does not pin a zone.
 */
const localDay = (iso: string) => new Intl.DateTimeFormat("en-US", {
    month: "short",
    day: "numeric",
    year: "numeric",
}).format(new Date(iso))

beforeEach(() => {
    mocks.job = finishedJob(65)
    mocks.push.mockClear()
    mocks.replace.mockClear()
    mocks.back.mockClear()
    mocks.fetchApi.mockReset()
    mocks.toast.error.mockClear()
    mocks.toast.success.mockClear()
    mocks.toast.warning.mockClear()
    mocks.fetchApiJson.mockReset().mockImplementation(async (url: string) => {
        if (url.includes("/logs")) return { id: "j1", workflow_id: "w1", logs: [] }
        if (isJobDetail(url)) return mocks.job
        if (url.includes("/workflows/w1")) return workflow
        throw new Error(`unexpected transport call: ${url}`)
    })
})

/**
 * jsdom implements no `EventSource`, and a running job subscribes to its live log
 * stream. Only the surface the viewer touches is stubbed: the subscription is
 * never expected to deliver anything here.
 */
class SilentEventSource {
    static CONNECTING = 0
    static OPEN = 1
    static CLOSED = 2

    readyState: number = SilentEventSource.OPEN
    onerror: (() => void) | null = null

    addEventListener() {}
    removeEventListener() {}
    close() {
        this.readyState = SilentEventSource.CLOSED
    }
}

afterEach(() => {
    cleanup()
    // The polling test below scopes fake timers to itself; this is the net for a
    // failure that leaves them installed, so no later test inherits a clock.
    vi.useRealTimers()
    vi.unstubAllGlobals()
})

it("shows the timeline of a finished job next to its identity and status", async () => {
    renderPage()

    // The timeline arrives with the job document.
    expect(await screen.findByRole("heading", { name: "Timeline" })).toBeTruthy()
    expect(screen.getByRole("heading", { name: "Job: j1" })).toBeTruthy()
    expect(screen.getByText("Workflow: w1")).toBeTruthy()
    expect(screen.getByText("Job Details")).toBeTruthy()

    // Every timeline row is filled in with the job's own timestamps, because the
    // job ran and finished.
    const runDay = localDay(startedAt)
    const finishedDay = localDay(mocks.job.completed_at as string)
    expect(timelineRow("Created")).toContain(runDay)
    expect(timelineRow("Scheduled")).toContain(runDay)
    expect(timelineRow("Started")).toContain(runDay)
    expect(timelineRow("Completed")).toContain(finishedDay)
    expect(timelineRow("Started")).not.toContain("Not started yet")
    expect(timelineRow("Completed")).not.toContain("Not completed yet")
    expect(renderedDuration()).toBe("1 minute 5 seconds")
    expect(screen.getByLabelText("Completed")).toBeTruthy()
})

it.each([
    // Seconds only: below the minute, with and without the singular spelling.
    ["same second", 0, "0 seconds"],
    ["one second", 1, "1 second"],
    ["under a minute", 59, "59 seconds"],
    // A completion stamped before the start is reported as the negative run it is.
    // The repository stamps both timestamps from Postgres, so no code path
    // produces this; a skewed pair renders this way and the page must not invent
    // an order the timestamps do not have.
    ["completion before the start", -5, "-5 seconds"],
    // Minutes: the minute count changes the whole unit, and so do the seconds.
    ["exactly a minute", 60, "1 minute 0 seconds"],
    ["one second into a minute", 61, "1 minute 1 second"],
    ["several minutes", 125, "2 minutes 5 seconds"],
    ["one second under an hour", 3599, "59 minutes 59 seconds"],
    // Hours: the hour count switches the unit, the remainder counts minutes, and
    // seconds stop being reported at all.
    ["exactly an hour", 3600, "1 hour 0 minutes"],
    ["an hour and a minute", 3660, "1 hour 1 minute"],
    // 59 seconds past the hour are dropped rather than rounded or carried.
    ["just under two hours", 3659, "1 hour 0 minutes"],
    ["two hours", 7200, "2 hours 0 minutes"],
    ["hours and minutes", 7261, "2 hours 1 minute"],
    ["hours with a dropped remainder", 7319, "2 hours 1 minute"],
])("reports a run of %s", async (_name, seconds, expected) => {
    mocks.job = finishedJob(seconds as number)

    renderPage()

    await waitFor(() => expect(renderedDuration()).toBe(expected as string))
})

it("labels a manually triggered job and asks the server again on refresh", async () => {
    mocks.job = { ...finishedJob(65), trigger: "MANUAL" }
    renderPage()
    await screen.findByRole("heading", { name: "Timeline" })

    // A job run by hand is not confused with a scheduled one.
    expect(screen.getByText("Manual")).toBeTruthy()
    expect(screen.queryByText("Automatic")).toBeNull()

    // Refresh is a real refetch of the job, not a repaint of what is on screen.
    const before = jobDetailRequests()
    await act(async () => {
        fireEvent.click(screen.getByRole("button", { name: "Refresh" }))
    })
    await waitFor(() => expect(jobDetailRequests()).toBe(before + 1))
    expect(screen.getByRole("heading", { name: "Timeline" })).toBeTruthy()
})

it("reports a failed load, then recovers the page through Try Again", async () => {
    failJobDetailTimes(1, new Error("job details unavailable: offline"))

    renderPage()

    // The failure replaces the page and says what the server said.
    expect(await screen.findByRole("heading", { name: "Error Loading Job" })).toBeTruthy()
    expect(screen.getByText("job details unavailable: offline")).toBeTruthy()
    expect(screen.queryByRole("heading", { name: "Timeline" })).toBeNull()
    expect(mocks.toast.error).toHaveBeenCalledWith("job details unavailable: offline")

    // Try Again retries the request, and the job comes back on its own.
    await act(async () => {
        fireEvent.click(screen.getByRole("button", { name: "Try Again" }))
    })
    expect(await screen.findByRole("heading", { name: "Timeline" })).toBeTruthy()
    expect(renderedDuration()).toBe("1 minute 5 seconds")
    expect(screen.queryByRole("heading", { name: "Error Loading Job" })).toBeNull()
})

it("keeps asking for a job that is still running, and stops once it has finished", async () => {
    // Fake timers are scoped to this test: the poll interval is driven by the clock
    // instead of by a real five-second wait. `shouldAdvanceTime` lets the clock
    // tick with real time, so the polling helpers still work while `settle` moves it
    // deliberately. `afterEach` restores the real clock.
    vi.useFakeTimers({ shouldAdvanceTime: true })
    vi.stubGlobal("EventSource", SilentEventSource)
    try {
        const settle = async (ms: number) => {
            await act(async () => {
                await vi.advanceTimersByTimeAsync(ms)
            })
        }

        let finished = false
        // A job that is still running has started but has not completed.
        const runningJob = (): Job => ({
            id: "j1",
            workflow_id: "w1",
            status: finished ? "COMPLETED" : "RUNNING",
            trigger: "AUTOMATIC",
            scheduled_at: startedAt,
            started_at: startedAt,
            ...(finished ? { completed_at: mocks.job.completed_at as string } : {}),
            created_at: startedAt,
            updated_at: startedAt,
        })
        mocks.fetchApiJson.mockImplementation(async (url: string) => {
            if (url.includes("/logs")) return { id: "j1", workflow_id: "w1", logs: [] }
            if (isJobDetail(url)) return runningJob()
            if (url.includes("/workflows/w1")) return workflow
            throw new Error(`unexpected transport call: ${url}`)
        })

        renderPage()
        await screen.findByRole("heading", { name: "Timeline" })

        // An unfinished run says so rather than inventing a completion time.
        expect(screen.getByLabelText("Running")).toBeTruthy()
        expect(timelineRow("Completed")).toBe("Not completed yet")
        expect(renderedDuration()).toBe("Not available")
        // `shouldAdvanceTime` lets wall-clock time move the fake clock, so the
        // counts that follow are read against a baseline instead of absolute
        // numbers: a slow runner may fit in an extra poll, and that must not read
        // as a broken page.
        const baseline = jobDetailRequests()
        expect(baseline).toBeGreaterThanOrEqual(1)

        // An active job is re-read on its own, without the reader asking. The
        // cadence comes from the policy the page reads, so a change to it shows up
        // here rather than silently making this test poll the wrong amount.
        await settle(queryRefetchIntervals.activeJob)
        await waitFor(() => expect(jobDetailRequests()).toBeGreaterThan(baseline))

        // Once the server reports the run finished, the page stops asking. The
        // count may still grow while the settled page is fetched, so the read
        // before the long settle only proves progress; the exact count is the
        // next one — polling has to stop, not merely slow down.
        finished = true
        await settle(queryRefetchIntervals.activeJob)
        await screen.findByLabelText("Completed")
        const settled = jobDetailRequests()
        expect(settled).toBeGreaterThan(baseline)
        await settle(queryRefetchIntervals.activeJob * 3)
        expect(jobDetailRequests()).toBe(settled)
        expect(renderedDuration()).toBe("1 minute 5 seconds")
    } finally {
        vi.useRealTimers()
    }
})

it("hands navigation back to the router when a reader gives up on a failed load", async () => {
    failJobDetailTimes(1, new Error("gone"))

    renderPage()

    // The page quotes the server's reason before offering a way out.
    expect(await screen.findByRole("heading", { name: "Error Loading Job" })).toBeTruthy()
    expect(screen.getByText("gone")).toBeTruthy()
    await act(async () => {
        fireEvent.click(screen.getByRole("button", { name: "Go Back" }))
    })
    // The mocked router does not navigate, so this pins the request the button
    // makes rather than an outcome only a real route change could produce.
    expect(mocks.back).toHaveBeenCalledTimes(1)
})

it("says plainly when a failure arrives without an error to quote", async () => {
    // A transport can fail with a bare value; the page must not render it raw.
    failJobDetailTimes(1, "gateway timeout")

    renderPage()

    expect(await screen.findByRole("heading", { name: "Error Loading Job" })).toBeTruthy()
    expect(screen.getByText("Failed to load job data")).toBeTruthy()
    expect(screen.queryByText("gateway timeout")).toBeNull()
})
