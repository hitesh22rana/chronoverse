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
    Activity,
    ScrollText,
} from "lucide-react"

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
import { EmptyState } from "@/components/empty-state"
import { UpdateWorkflowDialog } from "@/features/workflows/update-workflow-dialog"
import { TerminateWorkflowDialog } from "@/features/workflows/terminate-workflow-dialog"
import { DeleteWorkflowDialog } from "@/features/workflows/delete-workflow-dialog"
import { WorkflowDetailsActions } from "@/features/workflows/workflow-details-actions"
import { WorkflowDetailsCard } from "@/features/workflows/workflow-details-card"
import { WorkflowJobsToolbar } from "@/features/workflows/workflow-jobs-toolbar"
import { JobCard } from "@/features/jobs/job-card"
import { WorkflowJobsSkeleton } from "@/features/jobs/jobs-skeleton"
import { WorkflowAnalyticsCardsSkeleton } from "@/features/analytics/analytics-skeletons"

import { useWorkflowDetails } from "@/features/workflows/use-workflow-details"
import { useWorkflowJobs } from "@/features/jobs/use-workflow-jobs"
import type { Job } from "@/features/jobs/types"

import { cn } from "@/lib/utils"
import { getStatusMeta, getStatusLabel } from "@/features/jobs/job-status"
import { formatWorkflowInterval } from "@/features/workflows/workflow-schedule"

export default function WorkflowDetailsAndJobsPage() {
    return renderWorkflowDetailsAndJobsView(useWorkflowDetailsAndJobsModel())
}

function useWorkflowDetailsAndJobsModel() {
    const { workflowId } = useParams() as { workflowId: string }
    const [isSearchPending, startSearchTransition] = useTransition()
    const [isFiltersOpen, setIsFiltersOpen] = useState(false)

    const router = useRouter()
    const searchParams = useSearchParams()

    // Pending filter inputs; applied on Apply.
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

    const status = workflow?.terminated_at ? "TERMINATED" : workflow?.build_status

    const statusMeta = getStatusMeta(status)

    const interval = workflow?.interval ? formatWorkflowInterval(workflow.interval) : ""

    // Only the jobs toolbar mounts a refresh control, so this never runs on details.
    const handleRefresh = () => {
        refetchWorkflow()
        refetchJobs()
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

    const activeFiltersCount = [statusFilter, triggerFilter].filter(Boolean).length

    // The only state in which the page can act on the workflow. A failed refetch
    // keeps the last good payload in the cache, so `workflow` alone still looks
    // loaded; every consumer below must key off this instead, or the action
    // strip will offer buttons whose dialogs are not mounted.
    const hasLoadedWorkflow = !isWorkflowLoading && !workflowError

    return {
        isSearchPending,
        isFiltersOpen,
        filterState,
        setFilterState,
        urlTabFilter,
        workflow,
        isWorkflowLoading,
        hasLoadedWorkflow,
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
    }
}

function renderWorkflowDetailsAndJobsView(model: ReturnType<typeof useWorkflowDetailsAndJobsModel>) {
    const { urlTabFilter, handleTabsChange } = model

    return (
        <div className="flex flex-1 flex-col gap-6 h-full">
            {renderWorkflowHeader(model)}

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
                        className="cursor-pointer flex items-center justify-center gap-2 p-1.5 data-[state=active]:bg-background data-[state=active]:shadow-sm rounded-lg transition-[color,background-color,border-color,box-shadow]"
                    >
                        <ScrollText className="h-4 w-4" />
                        <span>Details</span>
                    </TabsTrigger>
                    <TabsTrigger
                        value="jobs"
                        className="cursor-pointer flex items-center justify-center gap-2 p-1.5 data-[state=active]:bg-background data-[state=active]:shadow-sm rounded-lg transition-[color,background-color,border-color,box-shadow]"
                    >
                        <Activity className="h-4 w-4" />
                        <span>Jobs</span>
                    </TabsTrigger>
                </TabsList>

                {renderWorkflowActions(model)}

                {renderWorkflowTabState(model)}

                {renderWorkflowDetails(model)}

                {renderWorkflowJobs(model)}
            </Tabs>
        </div>
    )
}

function WorkflowDetailsSkeleton() {
    return (
        <Card>
            <CardContent className="space-y-2">
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

                <div className="space-y-1 pt-4 pb-2">
                    <Skeleton className="h-3.5 w-24" />
                    <Skeleton className="h-[166px] w-full" />
                </div>

                <Separator />

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

function renderWorkflowHeader(model: Parameters<typeof renderWorkflowDetailsAndJobsView>[0]) {
    const { workflow, status, statusMeta } = model
    return (
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
                        <h1 className="text-2xl font-bold tracking-tight md:max-w-full max-w-68 w-full truncate">
                            {workflow?.name}
                        </h1>
                    ) : (
                        <Skeleton className="h-8 w-48" />
                    )}
                </div>
                <div className="flex items-center gap-2">
                    <Badge
                        variant="outline"
                        className={cn(
                            "px-2 py-0 h-5 font-medium flex items-center gap-1 border-none",
                            statusMeta.badgeClass,
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
    )
}

function renderWorkflowActions(model: Parameters<typeof renderWorkflowDetailsAndJobsView>[0]) {
    const {
        urlTabFilter,
        isSearchPending,
        workflow,
        hasLoadedWorkflow,
        isJobsLoading,
        isRefetchingJobs,
        filterState,
        isFiltersOpen,
        activeFiltersCount,
        isManualRunJobPending,
        manualRunJob,
        pagination,
        handleRefresh,
        handleApplyFilters,
        handleClearFilters,
        handleFiltersOpenChange,
        setFilterState,
        setShowUpdateWorkflowDialog,
        setShowTerminateWorkflowDialog,
        setShowDeleteWorkflowDialog,
    } = model

    if (urlTabFilter === "details") {
        return (
            <WorkflowDetailsActions
                workflow={hasLoadedWorkflow ? workflow : undefined}
                onEdit={() => setShowUpdateWorkflowDialog(true)}
                onTerminate={() => setShowTerminateWorkflowDialog(true)}
                onDelete={() => setShowDeleteWorkflowDialog(true)}
            />
        )
    }

    if (urlTabFilter === "jobs") {
        return (
            <WorkflowJobsToolbar
                workflow={workflow}
                filters={filterState}
                onFiltersChange={(patch) => setFilterState((previous) => ({ ...previous, ...patch }))}
                isFiltersOpen={isFiltersOpen}
                onFiltersOpenChange={handleFiltersOpenChange}
                activeFiltersCount={activeFiltersCount}
                onApplyFilters={handleApplyFilters}
                onClearFilters={handleClearFilters}
                onRefresh={handleRefresh}
                isRefreshPending={isSearchPending || isJobsLoading || isRefetchingJobs}
                isManualRunPending={isManualRunJobPending}
                onManualRun={() => manualRunJob()}
                hasNextPage={pagination.hasNextPage}
                hasPreviousPage={pagination.hasPreviousPage}
                onNextPage={() => pagination.goToNextPage()}
                onPreviousPage={() => pagination.goToPreviousPage()}
            />
        )
    }

    return null
}

function renderWorkflowTabState(model: Parameters<typeof renderWorkflowDetailsAndJobsView>[0]) {
    const { urlTabFilter, workflowError, jobs, isJobsLoading, jobsError, activeFiltersCount } = model
    return urlTabFilter === "details" && !!workflowError ? (
        <EmptyState title="Error loading workflow details" description="Please try again later.." />
    ) : urlTabFilter === "jobs" ? (
        jobsError ? (
            <EmptyState title="Error loading jobs" description="Please try again later." />
        ) : (
            !isJobsLoading &&
            jobs.length === 0 && (
                <EmptyState
                    title="No jobs found"
                    description={
                        activeFiltersCount > 0
                            ? "Try adjusting your search query or filters."
                            : "This workflow hasn't run any jobs yet."
                    }
                />
            )
        )
    ) : (
        urlTabFilter !== "details" &&
        urlTabFilter !== "jobs" && (
            <EmptyState title="Unknown tab" description="Please choose the correct tab" />
        )
    )
}

function renderWorkflowDetails(model: Parameters<typeof renderWorkflowDetailsAndJobsView>[0]) {
    const {
        urlTabFilter,
        workflow,
        isWorkflowLoading,
        hasLoadedWorkflow,
        workflowAnalytics,
        isAnalyticsLoading,
        isAnalyticsFetching,
        analyticsError,
        refetchAnalytics,
        showUpdateWorkflowDialog,
        setShowUpdateWorkflowDialog,
        showTerminateWorkflowDialog,
        setShowTerminateWorkflowDialog,
        showDeleteWorkflowDialog,
        setShowDeleteWorkflowDialog,
        status,
        statusMeta,
        interval,
    } = model
    return urlTabFilter === "details" && isWorkflowLoading ? (
        <WorkflowDetailsSkeleton />
    ) : (
        urlTabFilter === "details" && hasLoadedWorkflow && (
            <TabsContent value="details" className="h-full w-full">
                <UpdateWorkflowDialog
                    workflowId={workflow.id}
                    open={showUpdateWorkflowDialog}
                    onOpenChange={setShowUpdateWorkflowDialog}
                />

                <TerminateWorkflowDialog
                    workflow={workflow}
                    open={showTerminateWorkflowDialog}
                    onOpenChange={setShowTerminateWorkflowDialog}
                />

                <DeleteWorkflowDialog
                    workflow={workflow}
                    open={showDeleteWorkflowDialog}
                    onOpenChange={setShowDeleteWorkflowDialog}
                />

                <WorkflowDetailsCard
                    workflow={workflow}
                    status={status}
                    statusMeta={statusMeta}
                    interval={interval}
                    analytics={workflowAnalytics}
                    analyticsError={analyticsError}
                    isAnalyticsLoading={isAnalyticsLoading}
                    isAnalyticsFetching={isAnalyticsFetching}
                    onRetryAnalytics={() => refetchAnalytics()}
                />
            </TabsContent>
        )
    )
}

function renderWorkflowJobs(model: Parameters<typeof renderWorkflowDetailsAndJobsView>[0]) {
    const { urlTabFilter, jobs, isJobsLoading, jobsError } = model
    return urlTabFilter === "jobs" && isJobsLoading ? (
        <WorkflowJobsSkeleton />
    ) : (
        urlTabFilter === "jobs" && !isJobsLoading && !jobsError && !!jobs.length && (
            <TabsContent value="jobs" className="h-full w-full flex-1">
                <div className="grid grid-cols-1 xl:grid-cols-2 gap-4">
                    {jobs?.map((job: Job) => (
                        <JobCard key={job.id} job={job} />
                    ))}
                </div>
            </TabsContent>
        )
    )
}
