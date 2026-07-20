"use client"

import type { LucideIcon } from "lucide-react"
import {
    Activity,
    Clock3,
    Gauge,
    RefreshCw,
    ScrollText,
} from "lucide-react"

import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
    Card,
    CardAction,
    CardContent,
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

const metricSkeletons = [
    { id: "jobs", valueWidth: "w-20", showSecondLine: true },
    { id: "runtime", valueWidth: "w-24", showSecondLine: false },
    { id: "average", valueWidth: "w-16", showSecondLine: true },
    { id: "logs", valueWidth: "w-20", showSecondLine: false },
] as const

const decimalFormatter = new Intl.NumberFormat(undefined, { maximumFractionDigits: 1 })

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
    const logsPerJob = divide(totalLogs, totalJobs)

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
                {!isLoading && !error && analytics && (
                    <Badge variant="secondary">Lifetime</Badge>
                )}
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
                            value={totalJobs.toLocaleString()}
                            helper="Recorded after reaching a final state"
                            icon={Activity}
                        />
                        <AnalyticsMetricCard
                            label="Total runtime"
                            value={formatSeconds(totalRuntime)}
                            helper="Combined execution time"
                            icon={Clock3}
                        />
                        <AnalyticsMetricCard
                            label="Average runtime"
                            value={formatSeconds(Math.round(averageRuntime))}
                            helper="Per recorded job execution"
                            icon={Gauge}
                        />
                        <AnalyticsMetricCard
                            label="Retained logs"
                            value={totalLogs.toLocaleString()}
                            helper={logHelper(workflowKind, logRetention, logsPerJob)}
                            icon={ScrollText}
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

type AnalyticsMetricCardProps = {
    helper: string
    icon: LucideIcon
    label: string
    value: string
}

function AnalyticsMetricCard({ helper, icon: Icon, label, value }: AnalyticsMetricCardProps) {
    return (
        <Card className="min-h-39 h-full gap-4 py-4">
            <CardHeader className="grid grid-cols-[1fr_auto] px-4">
                <CardDescription>{label}</CardDescription>
                <Badge variant="secondary" className="size-8 p-0">
                    <Icon />
                </Badge>
            </CardHeader>
            <CardContent className="flex flex-col gap-1 px-4">
                <p className="text-2xl font-semibold tracking-tight tabular-nums">{value}</p>
                <p className="min-h-8 text-xs text-muted-foreground">{helper}</p>
            </CardContent>
        </Card>
    )
}

export function WorkflowAnalyticsCardsSkeleton() {
    return (
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-4">
            {metricSkeletons.map(({ id, valueWidth, showSecondLine }) => (
                <Card key={id} className="min-h-39 h-full gap-4 py-4">
                    <CardHeader className="grid grid-cols-[1fr_auto] px-4">
                        <Skeleton className="h-5 w-28" />
                        <Skeleton className="size-8 rounded-md" />
                    </CardHeader>
                    <CardContent className="flex flex-col gap-1 px-4">
                        <Skeleton className={cn("h-7", valueWidth)} />
                        <div className="flex min-h-8 flex-col gap-1">
                            <Skeleton className="h-3 w-full max-w-44" />
                            {showSecondLine && <Skeleton className="h-3 w-28" />}
                        </div>
                    </CardContent>
                </Card>
            ))}
        </div>
    )
}

function divide(numerator: number, denominator: number) {
    return denominator > 0 ? numerator / denominator : 0
}

function formatDecimal(value: number) {
    return decimalFormatter.format(value)
}

function logHelper(workflowKind: string, logRetention: boolean, logsPerJob: number) {
    if (workflowKind !== "CONTAINER") {
        return "Not emitted by this workflow kind"
    }

    if (!logRetention) {
        return "New logs are not retained"
    }

    return `${formatDecimal(logsPerJob)} entries per recorded job`
}
