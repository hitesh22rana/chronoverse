import { notFound } from "next/navigation"

import WorkflowDetailsAndJobsPage from "@/features/workflows/workflow-details-page"
import { isWorkflowRouteId } from "@/lib/workflow-route"

export default async function WorkflowPage({ params }: PageProps<"/workflows/[workflowId]">) {
    const { workflowId } = await params
    if (!isWorkflowRouteId(workflowId)) notFound()
    return <WorkflowDetailsAndJobsPage key={workflowId.toLowerCase()} />
}
