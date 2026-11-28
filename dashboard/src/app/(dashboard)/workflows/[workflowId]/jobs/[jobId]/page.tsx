import { notFound } from "next/navigation"

import JobDetailsAndLogsPage from "@/features/jobs/job-details-page"
import { isWorkflowRouteId } from "@/lib/workflow-route"

export default async function JobPage({ params }: { params: Promise<{ workflowId: string; jobId: string }> }) {
    const { workflowId, jobId } = await params
    if (!isWorkflowRouteId(workflowId) || !isWorkflowRouteId(jobId)) notFound()
    return <JobDetailsAndLogsPage key={`${workflowId.toLowerCase()}:${jobId.toLowerCase()}`} />
}
