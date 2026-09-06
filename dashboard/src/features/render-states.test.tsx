import { renderToStaticMarkup } from "react-dom/server"
import { beforeEach, expect, it, vi } from "vitest"
import { WorkflowAnalyticsPanel } from "./analytics/workflow-analytics-panel"
import { WorkflowCard } from "./workflows/workflow-card"
import JobDetailsAndLogsPage from "./jobs/job-details-page"
import { LogsViewer } from "./logs/logs-viewer"

const mocks = vi.hoisted(() => ({ logs: {} as Record<string, any>, job: {} as Record<string, any> }))
vi.mock("next/navigation", () => ({
    useParams: () => ({ workflowId: "w", jobId: "j" }),
    usePathname: () => "/workflows/w/jobs/j",
    useSearchParams: () => new URLSearchParams(),
    useRouter: () => ({}),
}))
vi.mock("./logs/use-job-logs", () => ({ useJobLogs: () => mocks.logs }))
vi.mock("./jobs/use-job-details", () => ({ useJobDetails: () => mocks.job }))
vi.mock("react-virtuoso", () => ({ Virtuoso: ({ totalCount }: { totalCount: number }) => <div>Rows: {totalCount}</div> }))

beforeEach(() => {
    mocks.logs = { logs: [], searchQuery: "", streamFilter: "", workflowKind: "CONTAINER" }
    mocks.job = {}
})

it.each([
    [{ isLoading: true, error: new Error("offline") }, 'data-slot="skeleton"'],
    [{ error: new Error("offline") }, "Analytics unavailable"],
    [{}, "No analytics yet"],
    [{ analytics: { workflow_id: "w", total_jobs: 2, total_joblogs: 8, total_job_execution_duration: 10 } }, "Job executions"],
])("preserves analytics state priority for %j", (state, text) => {
    const html = renderToStaticMarkup(<WorkflowAnalyticsPanel isLoading={false} logRetention={false} workflowKind="CONTAINER" onRetry={() => {}} {...state} />)
    expect(html).toContain(text)
    if ("isLoading" in state && state.isLoading) expect(html).not.toContain("Analytics unavailable")
})

it.each([
    [{ isLoading: true, error: new Error("offline") }, "RUNNING", "Loading logs..."],
    [{ error: new Error("offline"), logs: [{}] }, "RUNNING", "Error loading logs"],
    [{ logs: [{}], isRetentionDisabled: true }, "RUNNING", "Rows: 1"],
    [{ isLogsUnsupportedForKind: true, searchQuery: "x" }, "COMPLETED", "Logs are not available for"],
    [{ isRetentionDisabled: true, searchQuery: "x" }, "COMPLETED", "Log retention is disabled"],
    [{ searchQuery: "x" }, "COMPLETED", "No logs found"],
    [{}, "RUNNING", "Logs will appear here as the job executes"],
    [{}, "QUEUED", "Job is waiting to start"],
    [{}, "FAILED", "Job failed to execute"],
    [{}, "COMPLETED", "Job completed successfully"],
    [{}, "CANCELED", "This job did not produce any logs"],
])("preserves log state priority for %j / %s", (state, status, text) => {
    Object.assign(mocks.logs, state)
    expect(renderToStaticMarkup(<LogsViewer workflowId="w" jobId="j" jobStatus={status} completedAt="" />)).toContain(text)
})

it("renders unfinished and completed job timelines", () => {
    mocks.job = { job: { created_at: "2026-01-01T00:00:00Z", scheduled_at: "2026-01-01T00:00:00Z", status: "QUEUED" } }
    let html = renderToStaticMarkup(<JobDetailsAndLogsPage />)
    expect(html).toContain("Not started yet")
    expect(html).toContain("Not completed yet")
    expect(html).toContain("Not available")
    Object.assign(mocks.job.job, { started_at: "2026-01-01T00:00:00Z", completed_at: "2026-01-01T00:01:05Z" })
    html = renderToStaticMarkup(<JobDetailsAndLogsPage />)
    expect(html).toContain("1 minute 5 seconds")
    expect(html).not.toContain("Not started yet")
})

it("uses the same failure defaults for text and progress", () => {
    const html = renderToStaticMarkup(<WorkflowCard workflow={{ id: "w", name: "Workflow", kind: "CONTAINER", payload: "", build_status: "COMPLETED", interval: 60, log_retention: true, created_at: "2026-01-01", updated_at: "2026-01-01", max_consecutive_job_failures_allowed: 3 }} />)
    expect(html).toContain("0 / 3")
    expect(html).toContain("width:0%")
    expect(html).toContain("Runs every 1 hour")
})
