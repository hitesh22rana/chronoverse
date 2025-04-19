"use client"

import { useParams } from "next/navigation"

export default function JobDetailsPage() {
    const { workflowId, jobId } = useParams()
    console.log("Workflow ID:", workflowId)
    console.log("Job ID:", jobId)

    return (
        <div className="flex flex-col h-full">
            <h2 className="text-xl font-bold tracking-tight">Job Details</h2>
            <p className="text-muted-foreground">
                View and manage job details here.
            </p>
        </div>
    )
}