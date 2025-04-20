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
    CircleDashed,
    Shield,
    Settings,
    Filter,
    ChevronLeft,
    ChevronRight,
    ScrollText,
    Workflow,
    Edit,
    Trash2
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

import { useWorkflowDetails } from "@/hooks/use-workflow-details"
import { useWorkflowJobs, Job } from "@/hooks/use-workflow-jobs"

import { cn } from "@/lib/utils"

export default function WorkflowDetailsPage() {
    const { workflowId } = useParams() as { workflowId: string }

    const router = useRouter()
    const searchParams = useSearchParams()

    const urlStatusFilter = searchParams.get("status") || "ALL"
    const urlTabFilter = searchParams.get("tab") || "details"

    const {
        workflow,
        isLoading: isWorkflowLoading,
        error: workflowError,
        refetch: refetchWorkflow
    } = useWorkflowDetails(workflowId as string)

    const {
        jobs,
        isLoading: isJobsLoading,
        refetch: refetchJobs,
        error: jobsError,
        pagination
    } = useWorkflowJobs(workflowId as string)

    const [showUpdateWorkflowDialog, setShowUpdateWorkflowDialog] = useState(false)
    const [showTerminateWorkflowDialog, setShowTerminateWorkflowDialog] = useState(false)

    // Determine status
    const status = workflow?.terminated_at ? "TERMINATED" : workflow?.build_status

    // Get status configuration
    const statusConfig = getStatusConfig(status)

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
        const params = new URLSearchParams(searchParams.toString())

        if (value === "ALL") {
            params.delete("status")
        } else {
            params.set("status", value)
        }

        router.push(`?${params.toString()}`, { scroll: false })
    }

    return (
        <div className="flex flex-col gap-6 h-full">
            {/* Header */}
            <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-4">
                <div className="space-y-1">
                    <div className="flex items-center gap-2">
                        <Link
                            href="/"
                            className="h-8 w-8 border rounded-full flex items-center justify-center text-muted-foreground hover:bg-muted/50 transition-colors"
                        >
                            <ArrowLeft className="h-4 w-4" />
                        </Link>
                        {workflow?.name ? (
                            <h1 className="text-2xl font-bold tracking-tight">{workflow?.name}</h1>
                        ) : (
                            <Skeleton className="h-8 w-48" />
                        )}
                    </div>
                    <div className="flex items-center gap-2">
                        <Badge
                            variant="outline"
                            className={cn(
                                "px-2 py-0 h-5 font-medium flex items-center gap-1 border-none",
                                statusConfig.colorClass
                            )}
                        >
                            <statusConfig.icon className={cn("h-3 w-3", statusConfig.iconClass)} />
                            <span className="text-xs">{statusConfig.label}</span>
                        </Badge>
                        {workflow?.kind ? (
                            <Badge variant="secondary" className="px-2 py-0 h-5 text-xs font-normal">
                                {workflow?.kind}
                            </Badge>
                        ) : (
                            <Skeleton className="h-5 w-20" />
                        )}
                        {workflow?.created_at ? (
                            <span className="text-xs text-muted-foreground">
                                Created {formatDistanceToNow(new Date(workflow.created_at), { addSuffix: true })}
                            </span>
                        ) : (
                            <Skeleton className="h-4 w-32" />
                        )}
                    </div>
                </div>
            </div>

            {/* Tabs */}
            <Tabs
                defaultValue={urlTabFilter}
                className="w-full h-full"
                onValueChange={handleTabsChange}
            >
                <TabsList
                    className="grid h-max lg:max-w-xs w-full grid-cols-2 rounded-xl bg-muted/80 backdrop-blur-sm border-dashed border-muted/50"
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
                        <Workflow className="h-4 w-4" />
                        <span>Jobs</span>
                    </TabsTrigger>
                </TabsList>

                {/* Details Tab */}
                <TabsContent value="details" className="h-full w-full">
                    {isWorkflowLoading ? (
                        <WorkflowDetailsSkeleton />
                    ) : !!workflowError ? (
                        <EmptyState
                            title="Error loading workflow details"
                            description="Please try again later."
                        />
                    ) : (
                        <Fragment>
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

                            <div className="flex items-center justify-end mb-4 h-9 gap-5">
                                <Button
                                    variant="outline"
                                    size="sm"
                                    className="cursor-pointer shrink-0 max-w-[140px] w-full"
                                    onClick={() => setShowUpdateWorkflowDialog(true)}
                                >
                                    <Edit className="h-4 w-4" />
                                    Edit Workflow
                                </Button>

                                <Button
                                    variant="destructive"
                                    size="sm"
                                    className="cursor-pointer shrink-0 max-w-[180px] w-full"
                                    onClick={() => setShowTerminateWorkflowDialog(true)}
                                    disabled={!!workflow?.terminated_at}
                                >
                                    <Trash2 className="h-4 w-4" />
                                    Terminate Workflow
                                </Button>
                            </div>
                            <Card>
                                <CardHeader>
                                    <CardTitle className="text-base">Workflow Configuration</CardTitle>
                                </CardHeader>
                                <CardContent className="space-y-4">
                                    {/* Basic Info */}
                                    <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
                                        <div className="space-y-2">
                                            <div className="text-sm font-medium">Type</div>
                                            <div className="text-sm text-muted-foreground flex items-center gap-2">
                                                <Settings className="h-4 w-4" />
                                                {workflow?.kind}
                                            </div>
                                        </div>
                                        <div className="space-y-2">
                                            <div className="text-sm font-medium">Execution Schedule</div>
                                            <div className="text-sm text-muted-foreground flex items-center gap-2">
                                                <Clock className="h-4 w-4" />
                                                {interval}
                                            </div>
                                        </div>
                                        <div className="space-y-2">
                                            <div className="text-sm font-medium">Build Status</div>
                                            <div className="text-sm flex items-center gap-2">
                                                <span className={cn("flex items-center gap-1", statusConfig.colorClass)}>
                                                    <statusConfig.icon className={cn("h-4 w-4", statusConfig.iconClass)} />
                                                    {statusConfig.label}
                                                </span>
                                            </div>
                                        </div>
                                        <div className="space-y-2">
                                            <div className="text-sm font-medium">Max Consecutive Failures</div>
                                            <div className="text-sm text-muted-foreground flex items-center gap-2">
                                                <Shield className="h-4 w-4" />
                                                {workflow?.max_consecutive_job_failures_allowed}
                                            </div>
                                        </div>
                                    </div>

                                    <Separator />

                                    {/* Payload */}
                                    <div className="space-y-2">
                                        <div className="text-sm font-medium">Payload</div>
                                        <div className="text-sm text-muted-foreground">
                                            <pre className="bg-muted p-3 rounded-md overflow-auto text-xs">
                                                {workflow?.payload ? JSON.stringify(JSON.parse(workflow.payload), null, 2) : "No payload available"}
                                            </pre>
                                        </div>
                                    </div>

                                    {/* Failure tracking */}
                                    {workflow?.consecutive_job_failures_count ? (
                                        <Fragment>
                                            <Separator />
                                            <div className="space-y-2">
                                                <div className="flex items-center justify-between mb-1">
                                                    <div className="flex items-center text-amber-600 dark:text-amber-400">
                                                        <AlertTriangle className="h-3.5 w-3.5 mr-1.5" />
                                                        <span className="text-sm font-medium">Failure Tracking</span>
                                                    </div>
                                                    <span className="text-sm font-medium">
                                                        {workflow.consecutive_job_failures_count} / {workflow.max_consecutive_job_failures_allowed}
                                                    </span>
                                                </div>
                                                <div className="w-full bg-gray-200 dark:bg-gray-700 rounded-full h-1.5">
                                                    <div
                                                        className="bg-amber-500 h-1.5 rounded-full"
                                                        style={{
                                                            width: `${(workflow.consecutive_job_failures_count / workflow.max_consecutive_job_failures_allowed) * 100}%`
                                                        }}
                                                    />
                                                </div>
                                            </div>
                                        </Fragment>
                                    ) : (
                                        <Fragment>
                                            <Separator />
                                            <div className="flex items-center justify-between mb-1">
                                                <div className="flex items-center text-gray-500 dark:text-gray-400">
                                                    <CircleDashed className="h-3.5 w-3.5 mr-1.5" />
                                                    <span className="text-sm font-medium">Failure Tracking</span>
                                                </div>
                                                <span className="text-sm font-medium">No failures yet</span>
                                            </div>
                                        </Fragment>
                                    )}
                                </CardContent>
                                <CardFooter className="text-xs text-muted-foreground border-t">
                                    Last updated {formatDistanceToNow(new Date(workflow.updated_at), { addSuffix: true })}
                                </CardFooter>
                            </Card>
                        </Fragment>

                    )}
                </TabsContent>

                {/* Jobs Tab */}
                <TabsContent value="jobs" className="h-full w-full">
                    <div className="flex items-center justify-end gap-2 w-full mb-4">
                        {/* Updated status filter to use local state */}
                        <Select
                            value={urlStatusFilter}
                            onValueChange={handleStatusFilter}
                        >
                            <SelectTrigger className="sm:max-w-[150px] w-full h-9">
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
                                disabled={!pagination.hasPreviousPage || (urlStatusFilter !== "ALL")}
                                className="h-9 w-9"
                            >
                                <ChevronLeft className="size-4" />
                                <span className="sr-only">Previous page</span>
                            </Button>
                            <Button
                                variant="outline"
                                size="icon"
                                onClick={() => pagination.goToNextPage()}
                                disabled={!pagination.hasNextPage || (urlStatusFilter !== "ALL")}
                                className="h-9 w-9 ml-2"
                            >
                                <ChevronRight className="size-4" />
                                <span className="sr-only">Next page</span>
                            </Button>
                        </div>
                    </div>

                    {isJobsLoading ? (
                        <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
                            {Array(6).fill(0).map((_, i) => (
                                <JobCardSkeleton key={i} />
                            ))}
                        </div>
                    ) : !!jobsError ? (
                        <EmptyState
                            title="Error loading jobs"
                            description="Please try again later."
                        />
                    ) : jobs.length === 0 ? (
                        <EmptyState
                            title={`No ${urlStatusFilter.toLowerCase()} jobs found for this workflow.`}
                            description={urlStatusFilter !== "ALL"
                                ? 'Try adjusting your search or filters'
                                : 'This workflow hasn\'t run any jobs yet.'}
                        />
                    ) : (
                        <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
                            {/* <JobCardSkeleton /> */}
                            {jobs.map((job) => (
                                <JobCard key={job.id} job={job} />
                            ))}
                        </div>
                    )}
                </TabsContent>
            </Tabs>
        </div >
    )
}

function WorkflowDetailsSkeleton() {
    return (
        <Fragment>
            <div className="flex items-center justify-end mb-4 h-9 gap-5">
                <Skeleton className="h-9 max-w-[140px] w-full" />
                <Skeleton className="h-9 max-w-[180px] w-full" />
            </div>
            <Card className="overflow-hidden space-y-4">
                <CardHeader>
                    <Skeleton className="h-6 w-40" />
                </CardHeader>
                <CardContent className="grid grid-cols-1 md:grid-cols-2 gap-6 -mt-2">
                    {[...Array(4)].map((_, i) => (
                        <div key={i} className="space-y-2">
                            <Skeleton className="h-4 w-16" />
                            <div className="flex flex-row items-center gap-4">
                                <Skeleton className="h-4 w-4 rounded-full" />
                                <Skeleton className="h-4 w-28" />
                            </div>
                        </div>
                    ))}
                </CardContent>
                <Separator className="mx-6 -mt-6 mb-0" />
                <div className="flex flex-col gap-2 px-6 -mt-1">
                    <Skeleton className="h-4 w-16" />
                    <Skeleton className="h-36 w-full" />
                </div>
                <Separator className="mx-6 -mt-6 mb-0" />
                <div className="flex flex-row items-center justify-between w-full px-6">
                    <div className="flex flex-row items-center gap-4">
                        <Skeleton className="h-4 w-4 rounded-full" />
                        <Skeleton className="h-4 w-24" />
                    </div>
                    <Skeleton className="h-4 w-16" />
                </div>
                <Separator />
                <Skeleton className="h-4 w-36 mx-6 -mt-4" />
            </Card>
        </Fragment>
    )
}

// Function to get job status information
const getJobStatusInfo = (status: string) => {
    return {
        PENDING: {
            icon: CircleDashed,
            colorClass: "text-gray-600 bg-gray-50 dark:bg-gray-950/30",
            label: "Pending"
        },
        QUEUED: {
            icon: Clock,
            colorClass: "text-amber-600 bg-amber-50 dark:bg-amber-950/30",
            label: "Queued"
        },
        RUNNING: {
            icon: RefreshCw,
            colorClass: "text-blue-600 bg-blue-50 dark:bg-blue-950/30",
            iconClass: "animate-spin",
            label: "Running"
        },
        COMPLETED: {
            icon: CheckCircle,
            colorClass: "text-emerald-600 bg-emerald-50 dark:bg-emerald-950/30",
            label: "Completed"
        },
        FAILED: {
            icon: AlertTriangle,
            colorClass: "text-red-600 bg-red-50 dark:bg-red-950/30",
            label: "Failed"
        },
    }[status] || {
        icon: CircleDashed,
        colorClass: "text-gray-600 bg-gray-50 dark:bg-gray-950/30",
        label: status
    }
}

// Job Card Component remains unchanged
function JobCard({ job }: { job: Job }) {
    const statusInfo = getJobStatusInfo(job.status)
    const StatusIcon = statusInfo.icon

    return (
        <Link href={`/workflows/${job.workflow_id}/jobs/${job.id}`} className="block h-full">
            <Card className="overflow-hidden">
                <CardHeader className="pb-3">
                    <div className="flex items-center justify-between">
                        <div className="flex items-center gap-2">
                            <Badge
                                variant="outline"
                                className={cn(
                                    "px-2 py-0 h-5 font-medium flex items-center gap-1 border-none",
                                    statusInfo.colorClass
                                )}
                            >
                                <StatusIcon className={cn("h-3 w-3", statusInfo.iconClass)} />
                                <span className="text-xs">{statusInfo.label}</span>
                            </Badge>
                            <span className="text-sm font-medium">Job: {job.id.split('-')[0]}</span>
                        </div>
                        <span className="text-xs text-muted-foreground">
                            {job.created_at && formatDistanceToNow(new Date(job.created_at), { addSuffix: true })}
                        </span>
                    </div>
                </CardHeader>
                <CardContent className="pt-4 space-y-3">
                    <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
                        <div className="space-y-1">
                            <div className="text-xs text-muted-foreground">Scheduled</div>
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
                            <div className="text-xs text-muted-foreground">Started</div>
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
                            <div className="text-xs text-muted-foreground">Completed</div>
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

function JobCardSkeleton() {
    return (
        <Card className="overflow-hidden">
            <CardHeader className="pb-3">
                <div className="flex items-center justify-between">
                    <div className="flex items-center gap-2">
                        <Skeleton className="h-5 w-24" />
                        <Skeleton className="h-5 w-24" />
                    </div>
                    <Skeleton className="h-4 w-16" />
                </div>
            </CardHeader>
            <CardContent className="pt-4 space-y-3">
                <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
                    {[...Array(3)].map((_, i) => (
                        <Fragment key={i}>
                            <Skeleton className="h-3 w-16" />
                            <div className="flex items-center gap-1.5">
                                <Skeleton className="h-3 w-3" />
                                <Skeleton className="h-3 w-28" />
                            </div>
                        </Fragment>
                    ))}
                </div>
            </CardContent>
        </Card>
    )
}

// Status configuration function remains unchanged
function getStatusConfig(status: string) {
    return {
        QUEUED: {
            label: "Scheduled",
            icon: Clock,
            colorClass: "text-blue-500 bg-blue-50 dark:bg-blue-950/30",
            glowClass: "shadow-[0_0_15px_rgba(59,130,246,0.15)] dark:shadow-[0_0_20px_rgba(59,130,246,0.25)] border-blue-200/50 dark:border-blue-800/30",
            dotColor: "#3b82f6"
        },
        STARTED: {
            label: "Building",
            icon: RefreshCw,
            colorClass: "text-amber-500 bg-amber-50 dark:bg-amber-950/30",
            glowClass: "shadow-[0_0_15px_rgba(245,158,11,0.15)] dark:shadow-[0_0_20px_rgba(245,158,11,0.25)] border-amber-200/50 dark:border-amber-800/30",
            iconClass: "animate-spin",
            dotColor: "#f59e0b"
        },
        COMPLETED: {
            label: "Active",
            icon: CheckCircle,
            colorClass: "text-emerald-500 bg-emerald-50 dark:bg-emerald-950/30",
            glowClass: "shadow-[0_0_15px_rgba(16,185,129,0.15)] dark:shadow-[0_0_20px_rgba(16,185,129,0.25)] border-emerald-200/50 dark:border-emerald-800/30",
            dotColor: "#10b981"
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
        label: status || "Unknown",
        icon: CircleDashed,
        colorClass: "text-gray-500 bg-gray-50 dark:bg-gray-950/30",
        glowClass: "shadow-none",
        dotColor: "#6b7280"
    }
}