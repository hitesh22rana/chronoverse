"use client"

import { formatDistanceToNow } from "date-fns"
import { AlertTriangle, Clock, Database, HeartPulse, Shield, Workflow } from "lucide-react"

import { Badge } from "@/components/ui/badge"
import { Card, CardContent, CardFooter } from "@/components/ui/card"
import { Separator } from "@/components/ui/separator"

import { WorkflowAnalyticsPanel } from "@/features/analytics/workflow-analytics-panel"
import type { WorkflowAnalytics } from "@/features/analytics/types"
import type { StatusMeta } from "@/features/jobs/job-status"
import { getStatusLabel } from "@/features/jobs/job-status"
import type { Workflow as WorkflowRecord } from "@/features/workflows/types"

import { cn } from "@/lib/utils"

type WorkflowDetailsCardProps = {
    workflow: WorkflowRecord
    status?: string
    statusMeta: StatusMeta
    interval: string
    analytics?: WorkflowAnalytics
    analyticsError?: Error | null
    isAnalyticsLoading: boolean
    isAnalyticsFetching: boolean
    onRetryAnalytics: () => void
}

export function WorkflowDetailsCard({
    workflow,
    status,
    statusMeta,
    interval,
    analytics,
    analyticsError,
    isAnalyticsLoading,
    isAnalyticsFetching,
    onRetryAnalytics,
}: WorkflowDetailsCardProps) {
    return (
        <Card>
            <CardContent className="space-y-4">
                <div className="grid grid-cols-1 md:grid-cols-5 gap-4">
                    <div className="space-y-2">
                        <span className="text-sm font-medium">Workflow kind</span>
                        <div className="text-sm text-muted-foreground flex items-center gap-2">
                            {workflow.kind === "HEARTBEAT" ? (
                                <HeartPulse className="h-4 w-4" />
                            ) : (
                                <Workflow className="h-4 w-4" />
                            )}
                            {workflow.kind}
                        </div>
                    </div>
                    <div className="space-y-2">
                        <span className="text-sm font-medium">Execution schedule</span>
                        <div className="text-sm text-muted-foreground flex items-center gap-2">
                            <Clock className="h-4 w-4" />
                            {interval}
                        </div>
                    </div>
                    <div className="space-y-2">
                        <span className="text-sm font-medium">Status</span>
                        <Badge className={cn("text-sm flex items-center h-5", statusMeta.badgeClass)}>
                            <statusMeta.icon className={statusMeta.iconClass} />
                            {getStatusLabel(status, "workflow")}
                        </Badge>
                    </div>
                    <div className="space-y-2">
                        <span className="text-sm font-medium">Max consecutive failures allowed</span>
                        <div className="text-sm text-muted-foreground flex items-center gap-2">
                            <Shield className="h-4 w-4" />
                            {workflow.max_consecutive_job_failures_allowed}
                        </div>
                    </div>
                    <div className="space-y-2">
                        <span className="text-sm font-medium">Log retention</span>
                        <div className="text-sm text-muted-foreground flex items-center gap-2">
                            <Database className="h-4 w-4" />
                            {workflow.log_retention ? "Enabled" : "Disabled"}
                        </div>
                    </div>
                </div>

                <Separator />

                <div className="space-y-2">
                    <span className="text-sm font-medium">Configuration</span>
                    <div className="text-sm text-muted-foreground">
                        <pre className="bg-muted p-3 rounded-md overflow-auto text-xs">
                            {workflow.payload
                                ? JSON.stringify(JSON.parse(workflow.payload), null, 2)
                                : "No configuration available"}
                        </pre>
                    </div>
                </div>

                <Separator />

                <WorkflowAnalyticsPanel
                    analytics={analytics}
                    error={analyticsError}
                    isLoading={isAnalyticsLoading}
                    isFetching={isAnalyticsFetching}
                    logRetention={workflow.log_retention}
                    onRetry={() => onRetryAnalytics()}
                    workflowKind={workflow.kind}
                />

                <Separator />

                <div className="space-y-2">
                    <div className="flex items-center justify-between mb-1">
                        <div className="flex items-center text-orange-600 dark:text-orange-400">
                            <AlertTriangle className="h-3.5 w-3.5 mr-1.5" />
                            <span className="text-sm font-medium">Failure tracking</span>
                        </div>
                        {/* The API omits a zero failure count, so the fallback is load-bearing. */}
                        <span className="text-sm font-medium">
                            {workflow.consecutive_job_failures_count ?? 0} /{" "}
                            {workflow.max_consecutive_job_failures_allowed}
                        </span>
                    </div>
                    <div className="w-full bg-gray-200 dark:bg-gray-700 rounded-full h-1.5">
                        <div
                            className="bg-orange-500 h-1.5 rounded-full"
                            style={{
                                width: `${(workflow.consecutive_job_failures_count ?? 0) / workflow.max_consecutive_job_failures_allowed * 100}%`,
                            }}
                        />
                    </div>
                </div>
            </CardContent>
            <CardFooter className="text-xs text-muted-foreground border-t">
                <span className="ml-auto">
                    Last updated{" "}
                    {formatDistanceToNow(new Date(workflow.updated_at), { addSuffix: true })}
                </span>
            </CardFooter>
        </Card>
    )
}
