"use client"

import { useState } from "react"
import Link from "next/link"
import { format, formatDistanceToNow } from "date-fns"
import { Bot, Calendar, CheckCircle, Clock, Hand } from "lucide-react"

import { Card, CardContent, CardHeader } from "@/components/ui/card"
import { JobStatusBadge } from "@/components/dashboard/job-status-badge"
import type { Job } from "@/lib/api/types"
import { cn } from "@/lib/utils"

export function JobCard({ job }: { job: Job }) {
    const [isReasonVisible, setIsReasonVisible] = useState(false)

    return (
        <Link
            href={`/workflows/${job.workflow_id}/jobs/${job.id}`}
            prefetch={false}
            className="block h-full relative"
            onFocus={() => setIsReasonVisible(true)}
            onBlur={() => setIsReasonVisible(false)}
        >
            <Card className="overflow-hidden">
                <div className="absolute top-0.5 right-0.5 rotate-12 border-b border-b-amber-50">
                    {job.trigger === 'MANUAL' ? (
                        <Hand className="h-4 w-4" />
                    ) : (
                        <Bot className="h-4 w-4" />
                    )}
                </div>
                <CardHeader className="flex md:items-center items-start justify-between">
                    <div className="flex md:flex-row flex-col justify-start md:items-center items-start gap-2">
                        <JobStatusBadge
                            status={job.status}
                            reasonMessage={job.status_reason_message}
                            revealReason={isReasonVisible}
                            className="font-medium"
                        />
                        <span className="text-sm font-medium md:max-w-full max-w-44 w-full truncate">Job: {job.id}</span>
                    </div>
                    <span className="text-xs text-muted-foreground">
                        {job.created_at && formatDistanceToNow(new Date(job.created_at), { addSuffix: true })}
                    </span>
                </CardHeader>
                <CardContent className="md:pt-4 pt-0 space-y-3">
                    <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
                        <div className="space-y-1">
                            <span className="text-xs text-muted-foreground">Scheduled</span>
                            <div className="text-sm flex items-center gap-1.5">
                                <Calendar className="h-3.5 w-3.5 text-muted-foreground" />
                                {
                                    job.scheduled_at ?
                                        format(new Date(job.scheduled_at), "MMM d, yyyy HH:mm:ss") :
                                        <span className="text-gray-400">Not scheduled</span>
                                }
                            </div>
                        </div>

                        <div className="space-y-1">
                            <span className="text-xs text-muted-foreground">Started</span>
                            <div className="text-sm flex items-center gap-1.5">
                                <Clock className="h-3.5 w-3.5 text-muted-foreground" />
                                {
                                    job.started_at ?
                                        format(new Date(job.started_at), "MMM d, yyyy HH:mm:ss") :
                                        <span className="text-gray-400">Not started</span>
                                }
                            </div>
                        </div>

                        <div className="space-y-1">
                            <span className="text-xs text-muted-foreground">Completed</span>
                            <div className="text-sm flex items-center gap-1.5">
                                <CheckCircle className={cn(
                                    "h-3.5 w-3.5",
                                    job.status === "COMPLETED" ? "text-emerald-500" : "text-red-500"
                                )} />
                                {
                                    job.completed_at ?
                                        format(new Date(job.completed_at), "MMM d, yyyy HH:mm:ss") :
                                        <span className="text-gray-400">Not completed</span>
                                }
                            </div>
                        </div>
                    </div>
                </CardContent>
            </Card>
        </Link>
    )
}
