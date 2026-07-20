"use client"

import {
    useState,
    useTransition,
} from "react"
import Link from "next/link"
import { useParams, useRouter, useSearchParams } from "next/navigation"
import { formatDistanceToNow } from "date-fns"
import {
    ArrowLeft,
    RefreshCw,
    Clock,
    AlertTriangle,
    XCircle,
    Shield,
    Filter,
    ChevronLeft,
    ChevronRight,
    ScrollText,
    Workflow,
    Database,
    Edit,
    Trash2,
    HeartPulse,
    Activity,
    Play,
    Loader2,
    X,
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
} from "@/components/ui/card"
import {
    Select,
    SelectContent,
    SelectItem,
    SelectTrigger,
    SelectValue
} from "@/components/ui/select"
import {
    Popover,
    PopoverContent,
    PopoverTrigger,
} from "@/components/ui/popover"
import { Label } from "@/components/ui/label"
import { EmptyState } from "@/components/dashboard/empty-state"
import { UpdateWorkflowDialog } from "@/components/dashboard/update-workflow-dialog"
import { TerminateWorkflowDialog } from "@/components/dashboard/terminate-workflow-dialog"
import { DeleteWorkflowDialog } from "@/components/dashboard/delete-workflow-dialog"
import { JobCard } from "@/components/dashboard/workflow-job-card"
import { WorkflowJobsSkeleton } from "@/components/dashboard/workflow-jobs-skeleton"
import {
    WorkflowAnalyticsCardsSkeleton,
    WorkflowAnalyticsPanel,
} from "@/features/analytics/workflow"

import { useWorkflowDetails } from "@/hooks/use-workflow-details"
import { useWorkflowJobs } from "@/hooks/use-workflow-jobs"
import type { Job } from "@/lib/api/types"

import { cn } from "@/lib/utils"
import { getStatusMeta, getStatusLabel } from "@/lib/status"

export default function WorkflowDetailsAndJobsPage() {
    const { workflowId } = useParams() as { workflowId: string }
    const [isSearchPending, startSearchTransition] = useTransition()
    const [isFiltersOpen, setIsFiltersOpen] = useState(false)

    const router = useRouter()
    const searchParams = useSearchParams()

    // Local state for filter inputs (to be applied when "Apply Filters" is clicked)
    const [filterState, setFilterState] = useState({
        status: "",
        trigger: "",
    })

    const urlTabFilter = searchParams.get("tab") || "details"

    const {
        workflow,
        isLoading: isWorkflowLoading,
        error: workflowError,
        refetch: refetchWorkflow,
        workflowAnalytics,
        isAnalyticsLoading,
        isAnalyticsFetching,
        analyticsError,
        refetchAnalytics,
    } = useWorkflowDetails(workflowId, { analytics: urlTabFilter === "details" })

    const {
        jobs,
        isLoading: isJobsLoading,
        refetch: refetchJobs,
        isRefetching: isRefetchingJobs,
        error: jobsError,
        statusFilter,
        triggerFilter,
        applyAllFilters,
        clearAllFilters,
        pagination,
        manualRunJob,
        isManualRunJobPending,
    } = useWorkflowJobs(workflowId, { enabled: urlTabFilter === "jobs" })

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
        if (urlTabFilter === "details") {
            refetchAnalytics()
        } else {
            refetchJobs()
        }
    }

    const handleFiltersOpenChange = (nextOpen: boolean) => {
        if (nextOpen) {
            setFilterState({
                status: statusFilter || "",
                trigger: triggerFilter || "",
            })
        }
        setIsFiltersOpen(nextOpen)
    }

    // Handle tab change
    const handleTabsChange = (value: string) => {
        const params = new URLSearchParams(searchParams.toString())
        if (value === "details") {
            params.delete("tab")
            params.delete("cursor")
            params.delete("status")
            params.delete("trigger")
        } else {
            params.set("tab", value)
        }

        router.push(`?${params.toString()}`, { scroll: false })
    }

    const handleApplyFilters = () => {
        startSearchTransition(() => {
            applyAllFilters(filterState)
            setIsFiltersOpen(false)
        })
    }

    const handleClearFilters = () => {
        clearAllFilters()
        setFilterState({
            status: "",
            trigger: "",
        })
        setIsFiltersOpen(false)
    }

    // Count active filters
    const activeFiltersCount = [
        statusFilter,
        triggerFilter,
    ].filter(Boolean).length

    return renderWorkflowDetailsAndJobsView({
        isSearchPending,
        isFiltersOpen,
        filterState,
        setFilterState,
        urlTabFilter,
        workflow,
        isWorkflowLoading,
        workflowError,
        workflowAnalytics,
        isAnalyticsLoading,
        isAnalyticsFetching,
        analyticsError,
        refetchAnalytics,
        jobs,
        isJobsLoading,
        isRefetchingJobs,
        jobsError,
        pagination,
        manualRunJob,
        isManualRunJobPending,
        showUpdateWorkflowDialog,
        setShowUpdateWorkflowDialog,
        showTerminateWorkflowDialog,
        setShowTerminateWorkflowDialog,
        showDeleteWorkflowDialog,
        setShowDeleteWorkflowDialog,
        status,
        statusMeta,
        interval,
        handleRefresh,
        handleTabsChange,
        handleApplyFilters,
        handleClearFilters,
        handleFiltersOpenChange,
        activeFiltersCount,
    })
}

function renderWorkflowDetailsAndJobsView(model: any) {
    const {
        isSearchPending,
        isFiltersOpen,
        filterState,
        setFilterState,
        urlTabFilter,
        workflow,
        isWorkflowLoading,
        workflowError,
        workflowAnalytics,
        isAnalyticsLoading,
        isAnalyticsFetching,
        analyticsError,
        refetchAnalytics,
        jobs,
        isJobsLoading,
        isRefetchingJobs,
        jobsError,
        pagination,
        manualRunJob,
        isManualRunJobPending,
        showUpdateWorkflowDialog,
        setShowUpdateWorkflowDialog,
        showTerminateWorkflowDialog,
        setShowTerminateWorkflowDialog,
        showDeleteWorkflowDialog,
        setShowDeleteWorkflowDialog,
        status,
        statusMeta,
        interval,
        handleRefresh,
        handleTabsChange,
        handleApplyFilters,
        handleClearFilters,
        handleFiltersOpenChange,
        activeFiltersCount,
    } = model

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
                value={urlTabFilter}
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

                {urlTabFilter === "details" ? (
                    <div className="flex sm:flex-row flex-col items-center justify-end mb-4 gap-2 w-full">
                        <Button
                            variant="outline"
                            size="sm"
                            className="cursor-pointer shrink-0 sm:max-w-[140px] w-full h-9"
                            onClick={() => setShowUpdateWorkflowDialog(true)}
                        >
                            <Edit className="h-4 w-4" />
                            Edit workflow
                        </Button>
                        {isWorkflowLoading ? (
                            <Skeleton className="h-9 sm:max-w-[180px] w-full rounded-md" />
                        ) : workflow?.terminated_at ? (
                            <Button
                                variant="destructive"
                                size="sm"
                                className="cursor-pointer shrink-0 sm:max-w-[180px] w-full h-9"
                                onClick={() => setShowDeleteWorkflowDialog(true)}
                            >
                                <Trash2 className="h-4 w-4" />
                                Delete workflow
                            </Button>
                        ) : (
                            <Button
                                variant="secondary"
                                size="sm"
                                className="cursor-pointer shrink-0 sm:max-w-[180px] w-full h-9"
                                onClick={() => setShowTerminateWorkflowDialog(true)}
                            >
                                <XCircle className="h-4 w-4" />
                                Terminate workflow
                            </Button>
                        )}
                    </div>
                ) : urlTabFilter === "jobs" && (
                    <div className="flex items-center justify-end gap-2 w-full mb-4">
                        {/* Manual run */}
                        {!!workflow?.build_status && workflow.build_status === "COMPLETED" && (!workflow?.terminated_at) && (
                            <Button
                                variant="default"
                                size="sm"
                                className="cursor-pointer shrink-0 sm:max-w-[140px] w-full h-9"
                                onClick={() => manualRunJob()}
                                disabled={isManualRunJobPending}
                            >
                                {isManualRunJobPending ? (
                                    <Loader2 className="mr-2 h-4 w-4 animate-spin" />
                                ) : (
                                    <Play className="h-4 w-4" />
                                )}
                                Manual run
                            </Button>
                        )}

                        {/* Combined filters popover (trigger + status) */}
                        <Popover open={isFiltersOpen} onOpenChange={handleFiltersOpenChange}>
                            <PopoverTrigger asChild>
                                <Button variant="outline" className="relative h-9">
                                    <Filter className="size-3" />
                                    <span className="sm:not-sr-only sr-only">
                                        Filters
                                    </span>
                                    {activeFiltersCount > 0 && (
                                        <Badge
                                            variant="secondary"
                                            className="absolute -right-1 -top-1.5 size-4 rounded-full p-0 flex items-center justify-center text-xs overflow-visible"
                                        >
                                            {activeFiltersCount}
                                        </Badge>
                                    )}
                                </Button>
                            </PopoverTrigger>
                            <PopoverContent className="min-w-xs w-full m-2" align="center">
                                <div className="space-y-4">
                                    <div className="flex items-center justify-between">
                                        <h4 className="font-medium">Filter by</h4>
                                        {activeFiltersCount > 0 && (
                                            <Button
                                                variant="ghost"
                                                size="sm"
                                                onClick={handleClearFilters}
                                                className="h-8 text-muted-foreground hover:text-foreground"
                                            >
                                                <X className="size-3 mr-1" />
                                                Clear all
                                            </Button>
                                        )}
                                    </div>

                                    <Separator />

                                    <div className="flex flex-row gap-2 w-full">
                                        <div className="flex flex-col gap-2 w-full">
                                            <Label>Trigger</Label>
                                            <Select
                                                value={filterState.trigger || "ALL"}
                                                onValueChange={(value) =>
                                                    setFilterState((prev: { status: string; trigger: string }) => ({ ...prev, trigger: value === "ALL" ? "" : value }))}
                                            >
                                                <SelectTrigger className="w-full">
                                                    <SelectValue placeholder="All triggers" />
                                                </SelectTrigger>
                                                <SelectContent>
                                                    <SelectItem value="ALL">All triggers</SelectItem>
                                                    <SelectItem value="AUTOMATIC">Automatic</SelectItem>
                                                    <SelectItem value="MANUAL">Manual</SelectItem>
                                                </SelectContent>
                                            </Select>
                                        </div>

                                        <div className="flex flex-col gap-2 w-full">
                                            <Label>Status</Label>
                                            <Select
                                                value={filterState.status || "ALL"}
                                                onValueChange={(value) =>
                                                    setFilterState((prev: { status: string; trigger: string }) => ({ ...prev, status: value === "ALL" ? "" : value }))}
                                            >
                                                <SelectTrigger className="w-full">
                                                    <SelectValue placeholder="All statuses" />
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
                                        </div>
                                    </div>

                                    <Separator />

                                    {/* Apply button */}
                                    <Button onClick={handleApplyFilters} className="w-full">
                                        Apply Filters
                                    </Button>
                                </div>
                            </PopoverContent>
                        </Popover>

                        {/* Refresh Button */}
                        <Button
                            variant="outline"
                            size="icon"
                            onClick={handleRefresh}
                            disabled={(isSearchPending || isJobsLoading || isRefetchingJobs)}
                            className={cn(
                                "h-9 w-9",
                                (isSearchPending || isJobsLoading || isRefetchingJobs) && "cursor-not-allowed"
                            )}
                        >
                            <RefreshCw className={cn(
                                "size-4",
                                (isSearchPending || isJobsLoading || isRefetchingJobs) && "animate-spin"
                            )} />
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
                )}

                {urlTabFilter === "details" && !!workflowError ? (
                    <EmptyState
                        title="Error loading workflow details"
                        description="Please try again later.."
                    />
                ) : urlTabFilter === "jobs" ?
                    jobsError ? (
                        <EmptyState
                            title="Error loading jobs"
                            description="Please try again later."
                        />
                    ) : (!isJobsLoading && jobs.length === 0) && (
                        <EmptyState
                            title="No jobs found"
                            description={
                                activeFiltersCount > 0
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

                        <Card>
                            <CardContent className="space-y-4">
                                {/* Basic Info */}
                                <div className="grid grid-cols-1 md:grid-cols-5 gap-4">
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
                                    <div className="space-y-2">
                                        <span className="text-sm font-medium">Log retention</span>
                                        <div className="text-sm text-muted-foreground flex items-center gap-2">
                                            <Database className="h-4 w-4" />
                                            {workflow?.log_retention ? "Enabled" : "Disabled"}
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

                                <WorkflowAnalyticsPanel
                                    analytics={workflowAnalytics}
                                    error={analyticsError}
                                    isLoading={isAnalyticsLoading}
                                    isFetching={isAnalyticsFetching}
                                    logRetention={workflow.log_retention}
                                    onRetry={() => refetchAnalytics()}
                                    workflowKind={workflow.kind}
                                />

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
                        <div className="grid grid-cols-1 xl:grid-cols-2 gap-4">
                            {jobs?.map((job: Job) => (
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
        <Card>
            <CardContent className="space-y-2">
                {/* Basic Info Skeleton */}
                <div className="grid grid-cols-1 md:grid-cols-5 md:gap-4 gap-5 pb-2 pt-1">
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
                    <div className="space-y-2">
                        <Skeleton className="h-4 w-24" />
                        <div className="flex flex-row items-center gap-2">
                            <Skeleton className="h-4 w-4 rounded-full" />
                            <Skeleton className="h-3.5 w-16" />
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

                {/* Analytics Skeleton */}
                <div className="flex flex-col gap-3 py-2">
                    <div className="flex items-start justify-between gap-4">
                        <div className="flex flex-col gap-1 w-full">
                            <Skeleton className="h-5 w-36" />
                            <Skeleton className="h-4 w-full max-w-80" />
                        </div>
                        <Skeleton className="h-5 w-16 shrink-0 rounded-full" />
                    </div>
                    <WorkflowAnalyticsCardsSkeleton />
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
    )
}
