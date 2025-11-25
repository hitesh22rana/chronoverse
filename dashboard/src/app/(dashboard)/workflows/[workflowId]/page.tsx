"use client"

import {
    useEffect,
    useState,
    useTransition,
} from "react"
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
    Play,
    Loader2,
    Bot,
    Hand,
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

import { useWorkflowDetails } from "@/hooks/use-workflow-details"
import { useWorkflowJobs, Job } from "@/hooks/use-workflow-jobs"

import { cn, formatSeconds } from "@/lib/utils"
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
    } = useWorkflowDetails(workflowId)

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
    } = useWorkflowJobs(workflowId)

    const [showUpdateWorkflowDialog, setShowUpdateWorkflowDialog] = useState(false)
    const [showTerminateWorkflowDialog, setShowTerminateWorkflowDialog] = useState(false)
    const [showDeleteWorkflowDialog, setShowDeleteWorkflowDialog] = useState(false)

    // Sync filter state with current URL params
    useEffect(() => {
        setFilterState({
            status: statusFilter || "",
            trigger: triggerFilter || "",
        })
    }, [statusFilter, triggerFilter])

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
                        <Popover open={isFiltersOpen} onOpenChange={setIsFiltersOpen}>
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
                                                    setFilterState(prev => ({ ...prev, trigger: value === "ALL" ? "" : value }))}
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
                                                    setFilterState(prev => ({ ...prev, status: value === "ALL" ? "" : value }))}
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
                                                <div className="absolute inset-0 bg-linear-to-br from-blue-50 to-indigo-50 dark:from-blue-950/20 dark:to-indigo-950/20" />
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
                                                <div className="absolute inset-0 bg-linear-to-br from-emerald-50 to-green-50 dark:from-emerald-950/20 dark:to-green-950/20" />
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
                                                <div className="absolute inset-0 bg-linear-to-br from-amber-50 to-orange-50 dark:from-amber-950/20 dark:to-orange-950/20" />
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
    )
}

function JobCard({ job }: { job: Job }) {
    const statusMeta = getStatusMeta(job.status)
    const StatusIcon = statusMeta.icon

    return (
        <Link
            href={`/workflows/${job.workflow_id}/jobs/${job.id}`}
            prefetch={false}
            className="block h-full relative"
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
        <div className="grid grid-cols-1 xl:grid-cols-2 gap-4">
            {Array(10).fill(0).map((_, i) => (
                <JobCardSkeleton key={i} />
            ))}
        </div>
    )
}

function JobCardSkeleton() {
    return (
        <Card className="overflow-hidden relative">
            <div className="absolute top-0.5 right-0.5 rotate-12 border-b">
                <Skeleton className="h-4 w-4" />
            </div>
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