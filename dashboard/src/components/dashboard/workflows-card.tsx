"use client"

import Link from "next/link"
import { formatDistanceToNow } from "date-fns"
import { Clock, AlertTriangle } from "lucide-react"

import { Workflow } from "@/hooks/use-workflows"
import { cn } from "@/lib/utils"
import { getStatusMeta, getStatusLabel } from "@/lib/status"

import {
    Card,
    CardContent,
    CardFooter,
    CardHeader
} from "@/components/ui/card"
import { Badge } from "@/components/ui/badge"
import { Skeleton } from "@/components/ui/skeleton"

interface WorkflowCardProps {
    workflow: Workflow
}

export function WorkflowCard({ workflow }: WorkflowCardProps) {
    // Determine status
    const status = workflow?.terminated_at ? "TERMINATED" : workflow.build_status

    // Format dates
    const updatedAt = formatDistanceToNow(new Date(workflow.updated_at), { addSuffix: true })
    const statusMeta = getStatusMeta(status)

    const StatusIcon = statusMeta.icon

    // Format interval for display
    const interval = workflow.interval === 1440
        ? "daily"
        : workflow.interval % 60 === 0 && workflow.interval >= 60
            ? `every ${workflow.interval / 60} hour${workflow.interval / 60 !== 1 ? 's' : ''}`
            : `every ${workflow.interval} minute${workflow.interval !== 1 ? 's' : ''}`

    return (
        <Link href={`/workflows/${workflow.id}`} prefetch={false} className="block h-full">
            <Card className={cn(
                "h-full relative overflow-hidden transition-all duration-300 rounded-md",
                statusMeta.glowClass
            )}>
                {/* Status indicator dot */}
                <div
                    className="absolute top-3.5 right-3.5 h-2.5 w-2.5 rounded-full"
                    style={{ backgroundColor: statusMeta.dotColor }}
                />

                <CardHeader className="px-4">
                    <div className="flex flex-wrap items-center gap-1.5 mb-2">
                        <Badge
                            variant="outline"
                            className={cn(
                                "px-2 py-0 h-5 font-medium flex items-center gap-1 border-none",
                                statusMeta.badgeClass
                            )}
                        >
                            <StatusIcon className={cn("h-3 w-3", statusMeta.iconClass)} />
                            <span className="text-xs">{getStatusLabel(status, "workflow")}</span>
                        </Badge>

                        <Badge
                            variant="secondary"
                            className="px-2 py-0 h-5 text-xs font-normal"
                        >
                            {workflow.kind}
                        </Badge>
                    </div>

                    <h3 className="text-base font-semibold leading-tight line-clamp-1">
                        {workflow.name}
                    </h3>
                </CardHeader>

                <CardContent className="px-4">
                    <div className="flex items-center text-xs text-muted-foreground mt-1.5">
                        <Clock className="h-3.5 w-3.5 mr-1.5" />
                        <span>Runs {interval}</span>
                    </div>

                    <div className="mt-3">
                        <div className="flex items-center justify-between mb-1">
                            <div className="flex items-center text-orange-600 dark:text-orange-400">
                                <AlertTriangle className="h-3 w-3 mr-1" />
                                <span className="text-xs font-medium">Failures</span>
                            </div>
                            <span className="text-xs font-medium">
                                {workflow?.consecutive_job_failures_count ?? 0} / {workflow?.max_consecutive_job_failures_allowed ?? 1}
                            </span>
                        </div>

                        <div className="w-full bg-gray-200 dark:bg-gray-700 rounded-full h-1">
                            <div
                                className="bg-orange-500 h-1 rounded-full"
                                style={{
                                    width: `${(workflow?.consecutive_job_failures_count ?? 0) / (workflow?.max_consecutive_job_failures_allowed ?? 1) * 100}%`
                                }}
                            />
                        </div>
                    </div>
                </CardContent>

                {workflow.updated_at && (
                    <CardFooter className="px-4 border-t text-xs text-muted-foreground">
                        <span className="ml-auto">
                            Updated {updatedAt}
                        </span>
                    </CardFooter>
                )}
            </Card>
        </Link>
    )
}

export function WorkflowCardSkeleton() {
    return (
        <Card className="h-full relative overflow-hidden rounded-md shadow-sm">
            <CardHeader className="px-4">
                <div className="flex flex-wrap gap-1.5 mb-2">
                    <Skeleton className="h-5 w-20 rounded-full" />
                    <Skeleton className="h-5 w-16 rounded-full" />
                </div>
                <Skeleton className="h-6 w-3/4" />
            </CardHeader>

            <CardContent className="px-4">
                <div className="flex items-center mt-1.5">
                    <Skeleton className="h-3.5 w-3.5 mr-1.5 rounded-full" />
                    <Skeleton className="h-3.5 w-28" />
                </div>

                <div className="mt-3">
                    <div className="flex items-center justify-between mb-1">
                        <Skeleton className="h-3.5 w-16" />
                        <Skeleton className="h-3.5 w-8" />
                    </div>
                    <Skeleton className="h-1 w-full rounded-full" />
                </div>
            </CardContent>

            <CardFooter className="px-4 border-t">
                <Skeleton className="h-3.5 w-32 ml-auto" />
            </CardFooter>
        </Card>
    )
}