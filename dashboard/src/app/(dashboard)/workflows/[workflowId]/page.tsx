"use client"

import { Fragment, useState } from "react"
import Link from "next/link"
import { useParams, useRouter, useSearchParams } from "next/navigation"
import { formatDistanceToNow, format } from "date-fns"
import {
    ArrowLeft,
    RefreshCw,
    Clock,
    Calendar,
    AlertTriangle,
    CheckCircle,
    XCircle,
    Shield,
    Filter,
    ChevronLeft,
    ChevronRight,
    ScrollText,
    Workflow,
    Edit,
    Trash2,
    HeartPulse,
    Activity,
} from "lucide-react"

import { Button } from "@/components/ui/button"
import { Badge } from "@/components/ui/badge"
import { Separator } from "@/components/ui/separator"
import {
    Tabs,
    TabsList,
    TabsTrigger,
    TabsContent
} from "@/components/ui/tabs"
import { Skeleton } from "@/components/ui/skeleton"
import {
    Card,
    CardContent,
    CardFooter,
    CardHeader,
    CardTitle
} from "@/components/ui/card"
import {
    Select,
    SelectContent,
    SelectItem,
    SelectTrigger,
    SelectValue
} from "@/components/ui/select"
import { EmptyState } from "@/components/dashboard/empty-state"
import { UpdateWorkflowDialog } from "@/components/dashboard/update-workflow-dialog"
import { TerminateWorkflowDialog } from "@/components/dashboard/terminate-workflow-dialog"
import { DeleteWorkflowDialog } from "@/components/dashboard/delete-workflow-dialog"

import { useWorkflowDetails } from "@/hooks/use-workflow-details"
import { useWorkflowJobs, Job } from "@/hooks/use-workflow-jobs"

import { cn, formatSeconds } from "@/lib/utils"
import { getStatusMeta, getStatusLabel } from "@/lib/status"

export default function WorkflowDetailsAndJobsPage() {
    const { workflowId } = useParams() as { workflowId: string }

    const router = useRouter()
    const searchParams = useSearchParams()

    const urlStatusFilter = searchParams.get("status") || "ALL"
    const urlTabFilter = searchParams.get("tab") || "details"

    const {
        workflow,
        isLoading: isWorkflowLoading,
        error: workflowError,
        refetch: refetchWorkflow,
        workflowAnalytics,
        isAnalyticsLoading,
    } = useWorkflowDetails(workflowId)

    const {
        jobs,
        isLoading: isJobsLoading,
        refetch: refetchJobs,
        error: jobsError,
        applyAllFilters,
        pagination
    } = useWorkflowJobs(workflowId)

    const [showUpdateWorkflowDialog, setShowUpdateWorkflowDialog] = useState(false)
    const [showTerminateWorkflowDialog, setShowTerminateWorkflowDialog] = useState(false)
    const [showDeleteWorkflowDialog, setShowDeleteWorkflowDialog] = useState(false)

    // Determine status
    const status = workflow?.terminated_at ? "TERMINATED" : workflow?.build_status

    // Get status meta (unified)
    const statusMeta = getStatusMeta(status)

    // Format interval for display
    const interval = workflow?.interval
        ? workflow.interval === 1440
            ? "daily"
            : workflow.interval % 60 === 0 && workflow.interval >= 60
                ? `every ${workflow.interval / 60} hour${workflow.interval / 60 !== 1 ? 's' : ''}`
                : `every ${workflow.interval} minute${workflow.interval !== 1 ? 's' : ''}`
        : ""

    const handleRefresh = () => {
        refetchWorkflow()
        refetchJobs()
    }

    // Handle tab change
    const handleTabsChange = (value: string) => {
        const params = new URLSearchParams(searchParams.toString())
        if (value === "details") {
            params.delete("tab")
        } else {
            params.set("tab", value)
        }
        router.push(`?${params.toString()}`, { scroll: false })
    }

    // Handle status filter change
    const handleStatusFilter = (value: string) => {
        applyAllFilters({ status: value })
    }

    return (
        <div className="flex flex-1 flex-col gap-6 h-full">
            {/* Header */}
            <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-4">
                <div className="space-y-1">
                    <div className="flex items-center gap-2">
                        <Link
                            href="/"
                            prefetch={false}
                            className="h-8 w-8 px-2 border rounded-full flex items-center justify-center text-muted-foreground hover:bg-muted/50 transition-colors"
                        >
                            <ArrowLeft className="h-4 w-4" />
                        </Link>
                        {workflow?.name ? (
                            <h1 className="text-2xl font-bold tracking-tight md:max-w-full max-w-68 w-full truncate">{workflow?.name}</h1>
                        ) : (
                            <Skeleton className="h-8 w-48" />
                        )}
                    </div>
                    <div className="flex items-center gap-2">
                        <Badge
                            variant="outline"
                            className={cn(
                                "px-2 py-0 h-5 font-medium flex items-center gap-1 border-none",
                                statusMeta.badgeClass
                            )}
                        >
                            <statusMeta.icon className={cn("h-3 w-3", statusMeta.iconClass)} />
                            <span className="text-xs">{getStatusLabel(status, "workflow")}</span>
                        </Badge>
                        {workflow?.kind ? (
                            <Badge variant="secondary" className="px-2 py-0 h-5 text-xs font-normal">
                                {workflow?.kind}
                            </Badge>
                        ) : (
                            <Skeleton className="h-5 w-20" />
                        )}
                        {workflow?.created_at ? (
                            <span className="text-xs text-muted-foreground max-w-40 w-full truncate">
                                Created {formatDistanceToNow(new Date(workflow.created_at), { addSuffix: true })}
                            </span>
                        ) : (
                            <Skeleton className="h-4 w-32" />
                        )}
                    </div>
                </div>
            </div>

            <Tabs
                defaultValue={urlTabFilter}
                className="w-full h-full flex-1"
                onValueChange={handleTabsChange}
            >
                <TabsList
                    className="grid h-max lg:max-w-xs w-full grid-cols-2 rounded-xl bg-muted/80 backdrop-blur-sm border-dashed border-muted/50 p-1"
                >
                    <TabsTrigger
                        value="details"
                        className="cursor-pointer flex items-center justify-center gap-2 p-1.5 data-[state=active]:bg-background data-[state=active]:shadow-sm rounded-lg transition-all"
                    >
                        <ScrollText className="h-4 w-4" />
                        <span>Details</span>
                    </TabsTrigger>
                    <TabsTrigger
                        value="jobs"
                        className="cursor-pointer flex items-center justify-center gap-2 p-1.5 data-[state=active]:bg-background data-[state=active]:shadow-sm rounded-lg transition-all"
                    >
                        <Activity className="h-4 w-4" />
                        <span>Jobs</span>
                    </TabsTrigger>
                </TabsList>

                {urlTabFilter === "details" && !!workflowError ? (
                    <EmptyState
                        title="Error loading workflow details"
                        description="Please try again later.."
                    />
                ) : urlTabFilter === "jobs" ?
                    !!jobsError ? (
                        <EmptyState
                            title="Error loading jobs"
                            description="Please try again later."
                        />
                    ) : (!isJobsLoading && jobs.length === 0) && (
                        <EmptyState
                            title={
                                urlStatusFilter === "ALL"
                                    ? "No jobs found for this workflow"
                                    : `No ${urlStatusFilter.toLowerCase()} jobs found for this workflow`
                            }
                            description={
                                urlStatusFilter !== "ALL"
                                    ? "Try adjusting your search query or filters."
                                    : "This workflow hasn't run any jobs yet."
                            }
                        />
                    ) : urlTabFilter !== "details" && urlTabFilter !== "jobs" && (
                        <EmptyState
                            title="Unknown tab"
                            description="Please choose the correct tab"
                        />
                    )}

                {urlTabFilter === "details" && isWorkflowLoading ? (
                    <WorkflowDetailsSkeleton />
                ) : (urlTabFilter === "details" && !isWorkflowLoading && !workflowError) && (
                    // Details Tab
                    <TabsContent value="details" className="h-full w-full">
                        {/* UpdateWorkflow Dialog */}
                        <UpdateWorkflowDialog
                            workflowId={workflow.id}
                            open={showUpdateWorkflowDialog}
                            onOpenChange={setShowUpdateWorkflowDialog}
                        />

                        {/* TerminateWorkflow Dialog */}
                        <TerminateWorkflowDialog
                            workflow={workflow}
                            open={showTerminateWorkflowDialog}
                            onOpenChange={setShowTerminateWorkflowDialog}
                        />

                        {/* DeleteWorkflow Dialog */}
                        <DeleteWorkflowDialog
                            workflow={workflow}
                            open={showDeleteWorkflowDialog}
                            onOpenChange={setShowDeleteWorkflowDialog}
                        />

                        <div className="flex items-center justify-end mb-4 h-9 gap-5">
                            <Button
                                variant="outline"
                                size="sm"
                                className="cursor-pointer shrink-0 max-w-[140px] w-full"
                                onClick={() => setShowUpdateWorkflowDialog(true)}
                            >
                                <Edit className="h-4 w-4" />
                                Edit workflow
                            </Button>
                            {!!workflow?.terminated_at ? (
                                <Button
                                    variant="destructive"
                                    size="sm"
                                    className="cursor-pointer shrink-0 max-w-[180px] w-full"
                                    onClick={() => setShowDeleteWorkflowDialog(true)}
                                >
                                    <Trash2 className="h-4 w-4" />
                                    Delete workflow
                                </Button>
                            ) : (
                                <Button
                                    variant="secondary"
                                    size="sm"
                                    className="cursor-pointer shrink-0 max-w-[180px] w-full"
                                    onClick={() => setShowTerminateWorkflowDialog(true)}
                                >
                                    <XCircle className="h-4 w-4" />
                                    Terminate workflow
                                </Button>
                            )}
                        </div>
                        <Card>
                            <CardContent className="space-y-4">
                                {/* Basic Info */}
                                <div className="grid grid-cols-1 md:grid-cols-4 gap-4">
                                    <div className="space-y-2">
                                        <span className="text-sm font-medium">Workflow kind</span>
                                        <div className="text-sm text-muted-foreground flex items-center gap-2">
                                            {
                                                workflow?.kind === "HEARTBEAT" ?
                                                    <HeartPulse className="h-4 w-4" />
                                                    :
                                                    <Workflow className="h-4 w-4" />
                                            }
                                            {workflow?.kind}
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
                                        <Badge
                                            className={cn("text-sm flex items-center h-5",
                                                statusMeta.badgeClass
                                            )}>
                                            <statusMeta.icon className={statusMeta.iconClass} />
                                            {getStatusLabel(status, "workflow")}
                                        </Badge>
                                    </div>
                                    <div className="space-y-2">
                                        <span className="text-sm font-medium">Max consecutive failures allowed</span>
                                        <div className="text-sm text-muted-foreground flex items-center gap-2">
                                            <Shield className="h-4 w-4" />
                                            {workflow?.max_consecutive_job_failures_allowed}
                                        </div>
                                    </div>
                                </div>

                                <Separator />

                                {/* Configuration */}
                                <div className="space-y-2">
                                    <span className="text-sm font-medium">Configuration</span>
                                    <div className="text-sm text-muted-foreground">
                                        <pre className="bg-muted p-3 rounded-md overflow-auto text-xs">
                                            {workflow?.payload ? JSON.stringify(JSON.parse(workflow.payload), null, 2) : "No configuration available"}
                                        </pre>
                                    </div>
                                </div>

                                <Separator />

                                {/* Workflow Analytics */}
                                {!!workflowAnalytics && !isAnalyticsLoading ? (
                                    <div className="space-y-2">
                                        <span className="text-sm font-medium">Workflow Analytics</span>
                                        <div className="h-full w-full grid grid-cols-1 lg:grid-cols-3 gap-4">
                                            {/* Total Jobs Card */}
                                            <Card className="relative overflow-hidden">
                                                <div className="absolute inset-0 bg-gradient-to-br from-blue-50 to-indigo-50 dark:from-blue-950/20 dark:to-indigo-950/20" />
                                                <CardHeader className="relative flex flex-row items-center justify-between space-y-0 pb-2">
                                                    <CardTitle className="text-sm font-medium text-muted-foreground">
                                                        Total Jobs Executed
                                                    </CardTitle>
                                                    <div className="h-8 w-8 rounded-full bg-blue-100 dark:bg-blue-900/30 flex items-center justify-center">
                                                        <Activity className="h-4 w-4 text-blue-600 dark:text-blue-400" />
                                                    </div>
                                                </CardHeader>
                                                <CardContent className="relative">
                                                    <div className="text-3xl font-bold text-blue-700 dark:text-blue-300">
                                                        {workflowAnalytics?.total_jobs ? workflowAnalytics.total_jobs.toLocaleString() : "0"}
                                                    </div>
                                                    <p className="text-xs text-muted-foreground mt-1">
                                                        Jobs processed by this workflow
                                                    </p>
                                                </CardContent>
                                            </Card>

                                            {/* Total Logs Card */}
                                            <Card className="relative overflow-hidden">
                                                <div className="absolute inset-0 bg-gradient-to-br from-emerald-50 to-green-50 dark:from-emerald-950/20 dark:to-green-950/20" />
                                                <CardHeader className="relative flex flex-row items-center justify-between space-y-0 pb-2">
                                                    <CardTitle className="text-sm font-medium text-muted-foreground">
                                                        Total Log Entries Generated
                                                    </CardTitle>
                                                    <div className="h-8 w-8 rounded-full bg-emerald-100 dark:bg-emerald-900/30 flex items-center justify-center">
                                                        <ScrollText className="h-4 w-4 text-emerald-600 dark:text-emerald-400" />
                                                    </div>
                                                </CardHeader>
                                                <CardContent className="relative">
                                                    <div className="text-3xl font-bold text-emerald-700 dark:text-emerald-300">
                                                        {workflowAnalytics?.total_joblogs ? workflowAnalytics.total_joblogs.toLocaleString() : "0"}
                                                    </div>
                                                    <p className="text-xs text-muted-foreground mt-1">
                                                        Log entries across all jobs
                                                    </p>
                                                </CardContent>
                                            </Card>

                                            {/* Total Execution Time Card */}
                                            <Card className="relative overflow-hidden">
                                                <div className="absolute inset-0 bg-gradient-to-br from-amber-50 to-orange-50 dark:from-amber-950/20 dark:to-orange-950/20" />
                                                <CardHeader className="relative flex flex-row items-center justify-between space-y-0 pb-2">
                                                    <CardTitle className="text-sm font-medium text-muted-foreground">
                                                        Total Execution Time
                                                    </CardTitle>
                                                    <div className="h-8 w-8 rounded-full bg-amber-100 dark:bg-amber-900/30 flex items-center justify-center">
                                                        <Clock className="h-4 w-4 text-amber-600 dark:text-amber-400" />
                                                    </div>
                                                </CardHeader>
                                                <CardContent className="relative">
                                                    <div className="text-3xl font-bold text-amber-700 dark:text-amber-500">
                                                        {formatSeconds(workflowAnalytics?.total_job_execution_duration ?? 0)}
                                                    </div>
                                                    <p className="text-xs text-muted-foreground mt-1">
                                                        Total per job execution
                                                    </p>
                                                </CardContent>
                                            </Card>
                                        </div>
                                    </div>
                                ) : (
                                    <EmptyState
                                        title="No analytics available"
                                        description="This workflow has no analytics data yet."
                                    />
                                )}

                                <Separator />

                                {/* Failure tracking */}
                                <div className="space-y-2">
                                    <div className="flex items-center justify-between mb-1">
                                        <div className="flex items-center text-orange-600 dark:text-orange-400">
                                            <AlertTriangle className="h-3.5 w-3.5 mr-1.5" />
                                            <span className="text-sm font-medium">Failure tracking</span>
                                        </div>
                                        <span className="text-sm font-medium">
                                            {workflow?.consecutive_job_failures_count ?? 0} / {workflow?.max_consecutive_job_failures_allowed ?? 1}
                                        </span>
                                    </div>
                                    <div className="w-full bg-gray-200 dark:bg-gray-700 rounded-full h-1.5">
                                        <div
                                            className="bg-orange-500 h-1.5 rounded-full"
                                            style={{
                                                width: `${(workflow?.consecutive_job_failures_count ?? 0) / (workflow?.max_consecutive_job_failures_allowed ?? 1) * 100}%`
                                            }}
                                        />
                                    </div>
                                </div>
                            </CardContent>
                            <CardFooter className="text-xs text-muted-foreground border-t">
                                <span className="ml-auto">
                                    Last updated {formatDistanceToNow(new Date(workflow.updated_at), { addSuffix: true })}
                                </span>
                            </CardFooter>
                        </Card>
                    </TabsContent>
                )}

                {/* Jobs Tab */}
                {urlTabFilter === "jobs" && isJobsLoading ? (
                    <WorkflowJobsSkeleton />
                ) : (urlTabFilter === "jobs" && !isJobsLoading && !jobsError && !!jobs.length) && (
                    <TabsContent value="jobs" className="h-full w-full flex-1">
                        <div className="flex items-center justify-end gap-2 w-full mb-4">
                            {/* Updated status filter to use local state */}
                            <Select
                                value={urlStatusFilter}
                                onValueChange={handleStatusFilter}
                            >
                                <SelectTrigger className="sm:max-w-40 w-full h-9">
                                    <div className="flex items-center gap-2 text-sm">
                                        <Filter className="size-3.5" />
                                        <SelectValue placeholder="Filter by status" />
                                    </div>
                                </SelectTrigger>
                                <SelectContent>
                                    <SelectItem value="ALL">All statuses</SelectItem>
                                    <SelectItem value="PENDING">Pending</SelectItem>
                                    <SelectItem value="QUEUED">Queued</SelectItem>
                                    <SelectItem value="RUNNING">Running</SelectItem>
                                    <SelectItem value="COMPLETED">Completed</SelectItem>
                                    <SelectItem value="FAILED">Failed</SelectItem>
                                    <SelectItem value="CANCELED">Canceled</SelectItem>
                                </SelectContent>
                            </Select>

                            {/* Refresh Button */}
                            <Button
                                variant="outline"
                                size="sm"
                                className="shrink-0"
                                onClick={handleRefresh}
                            >
                                <RefreshCw className="h-4 w-4" />
                                <span className="sr-only">Refresh</span>
                            </Button>

                            {/* Pagination controls */}
                            <div className="flex items-center border-l pl-4 ml-1">
                                <Button
                                    variant="outline"
                                    size="icon"
                                    onClick={() => pagination.goToPreviousPage()}
                                    disabled={!pagination.hasPreviousPage}
                                    className="h-9 w-9"
                                >
                                    <ChevronLeft className="size-4" />
                                    <span className="sr-only">Previous page</span>
                                </Button>
                                <Button
                                    variant="outline"
                                    size="icon"
                                    onClick={() => pagination.goToNextPage()}
                                    disabled={!pagination.hasNextPage}
                                    className="h-9 w-9 ml-2"
                                >
                                    <ChevronRight className="size-4" />
                                    <span className="sr-only">Next page</span>
                                </Button>
                            </div>
                        </div>

                        <div className="grid grid-cols-1 xl:grid-cols-2 gap-4">
                            {jobs?.map((job) => (
                                <JobCard key={job.id} job={job} />
                            ))}
                        </div>
                    </TabsContent>
                )}
            </Tabs>
        </div>
    )
}

function WorkflowDetailsSkeleton() {
    return (
        <Fragment>
            <div className="flex items-center justify-end mb-2 h-9 gap-5">
                <Skeleton className="h-9 max-w-[140px] w-full" />
                <Skeleton className="h-9 max-w-[180px] w-full" />
            </div>
            <Card>
                <CardContent className="space-y-2">
                    {/* Basic Info Skeleton */}
                    <div className="grid grid-cols-1 md:grid-cols-4 md:gap-4 gap-5 pb-2 pt-1">
                        <div className="space-y-2">
                            <Skeleton className="h-4 w-24" />
                            <div className="flex flex-row items-center gap-2">
                                <Skeleton className="h-4 w-4 rounded-full" />
                                <Skeleton className="h-3.5 w-20" />
                            </div>
                        </div>
                        <div className="space-y-2">
                            <Skeleton className="h-4 w-28" />
                            <div className="flex flex-row items-center gap-2">
                                <Skeleton className="h-4 w-4 rounded-full" />
                                <Skeleton className="h-3.5 w-24" />
                            </div>
                        </div>
                        <div className="space-y-2">
                            <Skeleton className="h-4 w-14" />
                            <div className="flex flex-row items-center gap-2">
                                <Skeleton className="h-4 w-24 rounded-full" />
                            </div>
                        </div>
                        <div className="space-y-2">
                            <Skeleton className="h-4 w-40" />
                            <div className="flex flex-row items-center gap-2">
                                <Skeleton className="h-4 w-4 rounded-full" />
                                <Skeleton className="h-3.5 w-6" />
                            </div>
                        </div>
                    </div>

                    <Separator />

                    {/* Configuration Skeleton */}
                    <div className="space-y-1 pt-4 pb-2">
                        <Skeleton className="h-3.5 w-24" />
                        <Skeleton className="h-[166px] w-full" />
                    </div>

                    <Separator />

                    {/* Analytics Cards Skeleton */}
                    <div className="space-y-1 py-2">
                        <Skeleton className="h-5 w-36" />
                        <div className="h-full w-full grid grid-cols-1 lg:grid-cols-3 gap-4">
                            {/* Analytics Card Skeletons */}
                            {[...Array(3)].map((_, i) => (
                                <Card key={i} className="relative overflow-hidden">
                                    <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
                                        <Skeleton className="h-4 w-32" />
                                        <div className="h-8 w-8 rounded-full bg-muted">
                                            <Skeleton className="h-4 w-4 m-2" />
                                        </div>
                                    </CardHeader>
                                    <CardContent>
                                        <Skeleton className="h-8 w-16 mb-2" />
                                        <Skeleton className="h-4 w-36" />
                                    </CardContent>
                                </Card>
                            ))}
                        </div>
                    </div>

                    <Separator />

                    {/* Failure Tracking Skeleton */}
                    <div className="space-y-1 pt-2 pb-1">
                        <div className="flex items-center justify-between mb-1">
                            <div className="flex items-center gap-2">
                                <Skeleton className="h-4 w-4 rounded-full" />
                                <Skeleton className="h-4 w-24" />
                            </div>
                            <Skeleton className="h-4 w-16" />
                        </div>
                        <Skeleton className="h-1.5 w-full" />
                    </div>
                </CardContent>
                <CardFooter className="text-xs text-muted-foreground border-t">
                    <Skeleton className="h-4 w-52 ml-auto" />
                </CardFooter>
            </Card>
        </Fragment>
    )
}

function JobCard({ job }: { job: Job }) {
    const statusMeta = getStatusMeta(job.status)
    const StatusIcon = statusMeta.icon

    return (
        <Link
            href={`/workflows/${job.workflow_id}/jobs/${job.id}`}
            prefetch={false}
            className="block h-full"
        >
            <Card className="overflow-hidden">
                <CardHeader className="flex md:items-center items-start justify-between">
                    <div className="flex md:flex-row flex-col justify-start md:items-center items-start gap-2">
                        <Badge
                            variant="outline"
                            className={cn(
                                "px-2 py-1 font-medium flex items-center gap-1",
                                statusMeta.badgeClass
                            )}
                        >
                            <StatusIcon className={cn("h-3 w-3", statusMeta.iconClass)} />
                            <span className="text-xs">{getStatusLabel(job.status, "job")}</span>
                        </Badge>
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

function WorkflowJobsSkeleton() {
    return (
        <Fragment>
            <div className="flex flex-row items-center justify-end mb-2 h-9 gap-2 w-full">
                <div className="flex flex-row w-full gap-2 justify-end items-center">
                    <Skeleton className="sm:max-w-40 w-full h-9" />
                    <Skeleton className="h-8 w-12 sm:w-[38px] rounded-md" />
                </div>

                <div className="flex flex-row gap-2 items-center border-l pl-4 ml-1">
                    <Skeleton className="h-9 w-9" />
                    <Skeleton className="h-9 w-9" />
                </div>
            </div>
            <div className="grid grid-cols-1 xl:grid-cols-2 gap-4">
                {Array(10).fill(0).map((_, i) => (
                    <JobCardSkeleton key={i} />
                ))}
            </div>
        </Fragment>
    )
}

function JobCardSkeleton() {
    return (
        <Card className="overflow-hidden">
            <CardHeader className="flex md:items-center items-start justify-between md:pb-3.5 pb-2.5">
                <div className="flex md:flex-row flex-col justify-start md:items-center items-start gap-2">
                    <Skeleton className="h-6 w-24" />
                    <Skeleton className="h-5 md:w-80 w-44" />
                </div>
                <Skeleton className="h-4 w-28" />
            </CardHeader>
            <CardContent className="md:pt-4 pt-0 space-y-3">
                <div className="grid grid-cols-1 md:grid-cols-3 md:gap-4 gap-6">
                    {[...Array(3)].map((_, i) => (
                        <div key={i} className="md:space-y-1 space-y-2">
                            <Skeleton className="h-3 w-16" />
                            <div className="flex items-center gap-1.5">
                                <Skeleton className="h-3.5 w-3.5" />
                                <Skeleton className="h-4 w-28" />
                            </div>
                        </div>
                    ))}
                </div>
            </CardContent>
        </Card>
    )
}