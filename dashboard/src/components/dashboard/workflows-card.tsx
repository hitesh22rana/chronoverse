"use client"

import Link from "next/link"
import { formatDistanceToNow } from "date-fns"
import { Clock, CheckCircle, AlertTriangle, Loader2, XCircle, Shield } from "lucide-react"

import { Workflow } from "@/hooks/use-workflows"
import { cn } from "@/lib/utils"

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

// Status configuration with enhanced glow effects
const getStatusConfig = (status: string) => {
    return {
        COMPLETED: {
            label: "Active",
            icon: CheckCircle,
            colorClass: "text-emerald-500 bg-emerald-50 dark:bg-emerald-950/30",
            glowClass: "shadow-[0_0_15px_rgba(16,185,129,0.15)] dark:shadow-[0_0_20px_rgba(16,185,129,0.25)] border-emerald-200/50 dark:border-emerald-800/30",
            dotColor: "#10b981"
        },
        QUEUED: {
            label: "Scheduled",
            icon: Clock,
            colorClass: "text-blue-500 bg-blue-50 dark:bg-blue-950/30",
            glowClass: "shadow-[0_0_15px_rgba(59,130,246,0.15)] dark:shadow-[0_0_20px_rgba(59,130,246,0.25)] border-blue-200/50 dark:border-blue-800/30",
            dotColor: "#3b82f6"
        },
        STARTED: {
            label: "Building",
            icon: Loader2,
            colorClass: "text-amber-500 bg-amber-50 dark:bg-amber-950/30",
            glowClass: "shadow-[0_0_15px_rgba(245,158,11,0.15)] dark:shadow-[0_0_20px_rgba(245,158,11,0.25)] border-amber-200/50 dark:border-amber-800/30",
            iconClass: "animate-spin",
            dotColor: "#f59e0b"
        },
        FAILED: {
            label: "Failed",
            icon: AlertTriangle,
            colorClass: "text-red-500 bg-red-50 dark:bg-red-950/30",
            glowClass: "shadow-[0_0_15px_rgba(239,68,68,0.15)] dark:shadow-[0_0_20px_rgba(239,68,68,0.25)] border-red-200/50 dark:border-red-800/30",
            dotColor: "#ef4444"
        },
        CANCELED: {
            label: "Canceled",
            icon: XCircle,
            colorClass: "text-gray-500 bg-gray-50 dark:bg-gray-950/30",
            glowClass: "shadow-[0_0_15px_rgba(107,114,128,0.1)] dark:shadow-[0_0_15px_rgba(107,114,128,0.15)] border-gray-200/50 dark:border-gray-700/30",
            dotColor: "#6b7280"
        },
        TERMINATED: {
            label: "Terminated",
            icon: XCircle,
            colorClass: "text-red-500 bg-red-50 dark:bg-red-950/30",
            glowClass: "shadow-[0_0_15px_rgba(239,68,68,0.15)] dark:shadow-[0_0_20px_rgba(239,68,68,0.25)] border-red-200/50 dark:border-red-800/30",
            dotColor: "#ef4444"
        }
    }[status] || {
        label: status,
        icon: Clock,
        colorClass: "text-gray-500 bg-gray-50 dark:bg-gray-950/30",
        glowClass: "shadow-[0_0_15px_rgba(107,114,128,0.1)] dark:shadow-[0_0_15px_rgba(107,114,128,0.15)] border-gray-200/50 dark:border-gray-700/30",
        dotColor: "#6b7280"
    }
}

export function WorkflowCard({ workflow }: WorkflowCardProps) {
    // Determine status
    const status = workflow?.terminated_at ? "TERMINATED" : workflow.build_status

    // Format dates
    const updatedAt = formatDistanceToNow(new Date(workflow.updated_at), { addSuffix: true })
    const statusConfig = getStatusConfig(status)

    const StatusIcon = statusConfig.icon

    // Determine if we should show failures count
    const hasFailures = workflow.consecutive_job_failures_count && workflow.consecutive_job_failures_count > 0

    // Check if workflow has run at least once (updated_at different from created_at)
    const hasRun = workflow.updated_at !== workflow.created_at

    // Only show success badge if the workflow has run at least once and has no failures
    const showSuccessBadge = hasRun && !hasFailures && status === "COMPLETED"

    // Format interval for display
    const interval = workflow.interval === 1440
        ? "daily"
        : workflow.interval % 60 === 0 && workflow.interval >= 60
            ? `every ${workflow.interval / 60} hour${workflow.interval / 60 !== 1 ? 's' : ''}`
            : `every ${workflow.interval} minute${workflow.interval !== 1 ? 's' : ''}`

    return (
        <Link href={`/workflows/${workflow.id}`} className="block h-full">
            <Card className={cn(
                "h-full relative overflow-hidden transition-all duration-300 rounded-md",
                statusConfig.glowClass
            )}>
                {/* Status indicator dot */}
                <div
                    className="absolute top-3.5 right-3.5 h-2.5 w-2.5 rounded-full"
                    style={{ backgroundColor: statusConfig.dotColor }}
                />

                <CardHeader className="px-4">
                    <div className="flex flex-wrap items-center gap-1.5 mb-2">
                        <Badge
                            variant="outline"
                            className={cn(
                                "px-2 py-0 h-5 font-medium flex items-center gap-1 border-none",
                                statusConfig.colorClass
                            )}
                        >
                            <StatusIcon className={cn("h-3 w-3", statusConfig.iconClass)} />
                            <span className="text-xs">{statusConfig.label}</span>
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

                    {hasFailures ? (
                        <div className="mt-3">
                            <div className="flex items-center justify-between mb-1">
                                <div className="flex items-center text-amber-600 dark:text-amber-400">
                                    <AlertTriangle className="h-3 w-3 mr-1" />
                                    <span className="text-xs font-medium">Failures</span>
                                </div>
                                <span className="text-xs font-medium">
                                    {workflow.consecutive_job_failures_count} / {workflow.max_consecutive_job_failures_allowed || 0}
                                </span>
                            </div>

                            <div className="w-full bg-gray-200 dark:bg-gray-700 rounded-full h-1">
                                <div
                                    className="bg-amber-500 h-1 rounded-full"
                                    style={{
                                        width: `${((workflow.consecutive_job_failures_count || 0) / workflow.max_consecutive_job_failures_allowed) * 100}%`
                                    }}
                                />
                            </div>
                        </div>
                    ) : showSuccessBadge ? (
                        <div className="mt-3">
                            <div className="flex items-center justify-between">
                                <div className="flex items-center text-emerald-600 dark:text-emerald-400">
                                    <Shield className="h-3 w-3 mr-1" />
                                    <span className="text-xs font-medium">Healthy</span>
                                </div>
                                <span className="text-xs font-medium text-emerald-600 dark:text-emerald-400">
                                    0 failures
                                </span>
                            </div>

                            <div className="w-full bg-gray-200 dark:bg-gray-700 rounded-full h-1 mt-1">
                                <div className="bg-emerald-500 h-1 rounded-full w-full" />
                            </div>
                        </div>
                    ) : (
                        <div className="mt-3">
                            <div className="flex items-center justify-between mb-1">
                                <div className="flex items-center text-gray-500 dark:text-gray-400">
                                    <Shield className="h-3 w-3 mr-1" />
                                    <span className="text-xs font-medium">No runs yet</span>
                                </div>
                                <span className="text-xs font-medium text-gray-500 dark:text-gray-400">
                                    No failures
                                </span>
                            </div>

                            <div className="w-full bg-gray-200 dark:bg-gray-700 rounded-full h-1">
                                <div className="bg-gray-500 h-1 rounded-full w-full" />
                            </div>
                        </div>
                    )}
                </CardContent>

                {workflow.updated_at && (
                    <CardFooter className="px-4 border-t text-xs text-muted-foreground">
                        Updated {updatedAt}
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
                <Skeleton className="h-3.5 w-32" />
            </CardFooter>
        </Card>
    )
}