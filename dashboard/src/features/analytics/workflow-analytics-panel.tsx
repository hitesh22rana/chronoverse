"use client"

import {
    Activity,
    Clock3,
    Gauge,
    RefreshCw,
    ScrollText,
} from "lucide-react"

import { AnalyticsMetricCard } from "@/features/analytics/analytics-metric-card"
import { WorkflowAnalyticsCardsSkeleton } from "@/features/analytics/analytics-skeletons"
import {
    divide,
    formatInteger,
    formatRetainedLogsPerJob,
} from "@/features/analytics/analytics-utils"

import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
    Card,
    CardAction,
    CardDescription,
    CardHeader,
    CardTitle,
} from "@/components/ui/card"
import { Skeleton } from "@/components/ui/skeleton"

import type { WorkflowAnalytics } from "@/lib/api/types"
import { cn, formatSeconds } from "@/lib/utils"

type WorkflowAnalyticsPanelProps = {
    analytics?: WorkflowAnalytics
    error?: Error | null
    isLoading: boolean
    isFetching?: boolean
    logRetention: boolean
    onRetry: () => void
    workflowKind: string
}

export function WorkflowAnalyticsPanel({
    analytics,
    error,
    isLoading,
    isFetching = false,
    logRetention,
    onRetry,
    workflowKind,
}: WorkflowAnalyticsPanelProps) {
    const totalJobs = analytics?.total_jobs ?? 0
    const totalLogs = analytics?.total_joblogs ?? 0
    const totalRuntime = analytics?.total_job_execution_duration ?? 0
    const averageRuntime = divide(totalRuntime, totalJobs)

    return (
        <section className="flex flex-col gap-3" aria-labelledby="workflow-analytics-title">
            <div className="flex items-start justify-between gap-4">
                <div className="flex min-w-0 flex-col gap-1">
                    <h2 id="workflow-analytics-title" className="text-sm font-medium">
                        Workflow analytics
                    </h2>
                    <p className="text-xs text-muted-foreground">
                        Lifetime execution signals from the existing workflow aggregate
                    </p>
                </div>
                {isLoading ? (
                    <Skeleton className="h-5 w-16 shrink-0 rounded-full" />
                ) : !error && analytics ? (
                    <Badge variant="secondary">Lifetime</Badge>
                ) : null}
            </div>

            {isLoading ? (
                <WorkflowAnalyticsCardsSkeleton />
            ) : error ? (
                <Card className="gap-4 py-4">
                    <CardHeader className="px-4">
                        <CardTitle className="text-sm">Analytics unavailable</CardTitle>
                        <CardDescription>{error.message}</CardDescription>
                        <CardAction>
                            <Button variant="outline" size="sm" onClick={onRetry} disabled={isFetching}>
                                <RefreshCw data-icon="inline-start" className={cn(isFetching && "animate-spin")} />
                                Try again
                            </Button>
                        </CardAction>
                    </CardHeader>
                </Card>
            ) : analytics ? (
                <>
                    <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-4">
                        <AnalyticsMetricCard
                            label="Job executions"
                            value={formatInteger(totalJobs)}
                            helper="Recorded after reaching a final state"
                            icon={Activity}
                            variant="workflow"
                        />
                        <AnalyticsMetricCard
                            label="Total runtime"
                            value={formatSeconds(totalRuntime)}
                            helper="Combined execution time"
                            icon={Clock3}
                            variant="workflow"
                        />
                        <AnalyticsMetricCard
                            label="Average runtime"
                            value={formatSeconds(Math.round(averageRuntime))}
                            helper="Per recorded job execution"
                            icon={Gauge}
                            variant="workflow"
                        />
                        <AnalyticsMetricCard
                            label="Retained logs"
                            value={formatInteger(totalLogs)}
                            helper={getLogHelper(workflowKind, logRetention, totalLogs, totalJobs)}
                            icon={ScrollText}
                            variant="workflow"
                        />
                    </div>
                    {!logRetention && (
                        <p className="flex items-center gap-2 text-xs text-muted-foreground">
                            <ScrollText className="size-3.5 shrink-0" />
                            {workflowKind === "CONTAINER"
                                ? "New execution logs are not retained for this workflow."
                                : "This workflow kind does not produce retained execution logs."}
                        </p>
                    )}
                </>
            ) : (
                <Card className="gap-4 py-4">
                    <CardHeader className="px-4">
                        <CardTitle className="text-sm">No analytics yet</CardTitle>
                        <CardDescription>
                            Metrics will appear after this workflow records its first completed execution.
                        </CardDescription>
                    </CardHeader>
                </Card>
            )}
        </section>
    )
}

function getLogHelper(workflowKind: string, logRetention: boolean, totalLogs: number, totalJobs: number) {
    if (workflowKind !== "CONTAINER") {
        return "Not emitted by this workflow kind"
    }

    if (!logRetention) {
        return "New logs are not retained"
    }

    return formatRetainedLogsPerJob(totalLogs, totalJobs) ?? "No retained logs recorded"
}
