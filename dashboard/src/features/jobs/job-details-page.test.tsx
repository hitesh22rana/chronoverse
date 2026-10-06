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
import { cleanup, render, screen, waitFor } from "@testing-library/react"
import { afterEach, beforeEach, expect, it, vi } from "vitest"

import type { Job } from "@/features/jobs/types"
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

/** The duration cell of the timeline, which is the only row reading a duration. */
const renderedDuration = () => screen.getByText("Duration:").parentElement?.textContent?.slice("Duration:".length)

const timelineRow = (label: string) => screen.getByText(`${label}:`).parentElement?.textContent

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
        if (url.startsWith("/workflows/w1/jobs/")) return mocks.job
        if (url.startsWith("/workflows/w1")) return workflow
        throw new Error(`unexpected transport call: ${url}`)
    })
})

afterEach(() => {
    cleanup()
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
    // Minutes: the minute count changes the whole unit, and so do the seconds.
    ["exactly a minute", 60, "1 minute 0 seconds"],
    ["one second into a minute", 61, "1 minute 1 second"],
    ["several minutes", 125, "2 minutes 5 seconds"],
    ["one second under an hour", 3599, "59 minutes 59 seconds"],
    // Hours: the hour count switches the unit and the remainder counts minutes.
    ["exactly an hour", 3600, "1 hour 0 minutes"],
    ["an hour and a minute", 3660, "1 hour 1 minute"],
    ["two hours", 7200, "2 hours 0 minutes"],
    ["hours and minutes", 7261, "2 hours 1 minute"],
])("reports a run of %s", async (_name, seconds, expected) => {
    mocks.job = finishedJob(seconds as number)

    renderPage()

    await waitFor(() => expect(renderedDuration()).toBe(expected as string))
})
