import { renderToStaticMarkup } from "react-dom/server"
import { expect, it, vi } from "vitest"

import WorkflowPage from "./(dashboard)/workflows/[workflowId]/page"
import JobPage from "./(dashboard)/workflows/[workflowId]/jobs/[jobId]/page"

vi.mock("@/features/workflows/workflow-details-page", () => ({ default: () => <p>Workflow details</p> }))
vi.mock("@/features/jobs/job-details-page", () => ({ default: () => <p>Job details</p> }))

const workflowId = "AB000000-0000-0000-0000-000000000001"
const jobId = "AB000000-0000-0000-0000-000000000002"

it("mounts valid workflow and job routes, including uppercase UUIDs", async () => {
    expect(renderToStaticMarkup(await WorkflowPage({ params: Promise.resolve({ workflowId }) }))).toContain("Workflow details")
    expect(renderToStaticMarkup(await JobPage({ params: Promise.resolve({ workflowId, jobId }) }))).toContain("Job details")
})

it("rejects invalid workflow and job route IDs before mounting a protected view", async () => {
    await expect(WorkflowPage({ params: Promise.resolve({ workflowId: "wrong" }) })).rejects.toThrow("404")
    await expect(JobPage({ params: Promise.resolve({ workflowId, jobId: "wrong" }) })).rejects.toThrow("404")
    await expect(JobPage({ params: Promise.resolve({ workflowId: "wrong", jobId }) })).rejects.toThrow("404")
})

it("identifies the current workflow only on valid workflow and job paths", async () => {
    const { workflowIdFromPath } = await import("@/lib/workflow-route")
    for (const path of ["/", "/workflows/not-a-uuid", "/workflows/%ZZ", `/elsewhere/${workflowId}`]) {
        expect(workflowIdFromPath(path)).toBeUndefined()
    }
    expect(workflowIdFromPath(`/workflows/%41${workflowId.slice(1)}/jobs/${jobId}`))
        .toBe(workflowId.toLowerCase())
})
