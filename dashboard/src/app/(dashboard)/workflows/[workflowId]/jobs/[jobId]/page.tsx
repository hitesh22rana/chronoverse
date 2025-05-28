"use client"

import { Fragment } from "react"
import Link from "next/link"
import { useParams, useRouter } from "next/navigation"
import { format, formatDistanceToNow } from "date-fns"
import {
    RefreshCw,
    ArrowLeft,
    Loader2,
    AlertTriangle,
    CheckCircle,
    XCircle,
    Clock,
} from "lucide-react"

import { Button } from "@/components/ui/button"
import {
    Card,
    CardContent,
    CardHeader,
    CardTitle
} from "@/components/ui/card"
import { Badge } from "@/components/ui/badge"
import { Skeleton } from "@/components/ui/skeleton"

import { useJobDetails } from "@/hooks/use-job-details"

import { cn } from "@/lib/utils"
import { LogViewer } from "@/components/dashboard/log-viewer"

export default function JobDetailsPage() {
    const { workflowId, jobId } = useParams() as { workflowId: string, jobId: string }
    const router = useRouter()

    // Fetch job details and logs
    const {
        job,
        isLoading: isJobLoading,
        error: jobError,
        refetch: refetchJob
    } = useJobDetails(workflowId, jobId)

    // Handle manual refresh
    const handleRefresh = () => {
        refetchJob()
    }

    if (jobError) {
        return (
            <div className="flex flex-col items-center justify-center h-full p-8">
                <AlertTriangle className="h-12 w-12 text-red-500 mb-4" />
                <h2 className="text-xl font-bold mb-2">Error Loading Job</h2>
                <p className="text-muted-foreground mb-6">
                    {jobError instanceof Error ? jobError.message :
                        "Failed to load job data"}
                </p>
                <div className="flex gap-4">
                    <Button className="cursor-pointer" onClick={() => router.back()}>
                        Go Back
                    </Button>
                    <Button variant="outline" className="cursor-pointer" onClick={handleRefresh}>
                        Try Again
                    </Button>
                </div>
            </div>
        )
    }

    return (
        <div className="flex flex-col h-full space-y-6">
            {/* Header with back button */}
            <div className="flex items-center justify-between">
                <div className="flex items-center gap-2">
                    <Link
                        href={`/workflows/${workflowId}`}
                        className="h-8 w-8 border rounded-full flex items-center justify-center text-muted-foreground hover:bg-muted/50 transition-colors"
                    >
                        <ArrowLeft className="h-4 w-4" />
                    </Link>
                    <div>
                        <h2 className="text-xl font-bold tracking-tight md:max-w-full max-w-60 w-full truncate">Job: {jobId}</h2>
                        <p className="text-sm text-muted-foreground md:max-w-full max-w-60 w-full truncate">
                            Workflow: {workflowId}
                        </p>
                    </div>
                </div>

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
            </div>

            {/* Simple two-column grid layout */}
            <div className="grid grid-row-2 w-full gap-4">
                {/* Details Panel */}
                <Card className="overflow-auto">
                    <CardHeader className="pb-2">
                        <div className="flex items-center justify-between">
                            <CardTitle>Job Details</CardTitle>
                            {job ? (
                                <Badge className={cn(getStatusInfo(job.status).color, "flex items-center gap-1 px-2 py-1")}>
                                    {getStatusInfo(job.status).icon}
                                    {job.status}
                                </Badge>
                            ) : (
                                <Badge className="bg-gray-100 text-gray-700 border-gray-200 dark:bg-gray-900/30 dark:text-gray-400 dark:border-gray-800/30 h-6.5">
                                    <Loader2 className="h-4 w-4 animate-spin" />
                                    Loading...
                                </Badge>
                            )}
                        </div>
                        {job && (
                            <p className="text-sm text-muted-foreground">
                                Created {formatDistanceToNow(new Date(job.created_at), { addSuffix: true })}
                            </p>
                        )}
                    </CardHeader>
                    <CardContent className="space-y-6">
                        {isJobLoading ? (
                            <Fragment>
                                <Skeleton className="h-5 w-36 -mt-8" />
                                <Skeleton className="h-5 w-20 mt-8" />
                                {[...Array(3)].map((_, index) => (
                                    <div key={index} className="flex flex-row w-full items-center justify-between -mt-2">
                                        <Skeleton className="h-5 w-24" />
                                        <Skeleton className="h-5 w-40" />
                                    </div>
                                ))}
                                <div className="flex flex-row w-full items-center justify-between -mt-2">
                                    <Skeleton className="h-5 w-24" />
                                    <Skeleton className="h-5 w-20" />
                                </div>
                            </Fragment>
                        ) : job && (
                            <Fragment>
                                {/* Timing Information */}
                                <div className="space-y-3">
                                    <h3 className="font-medium text-sm">Timeline</h3>
                                    <div className="space-y-2">
                                        <div className="flex justify-between text-sm">
                                            <span className="text-muted-foreground">Created:</span>
                                            <span>{format(new Date(job.created_at), "MMM d, yyyy HH:mm:ss")}</span>
                                        </div>
                                        <div className="flex justify-between text-sm">
                                            <span className="text-muted-foreground">Scheduled:</span>
                                            <span>{format(new Date(job.scheduled_at), "MMM d, yyyy HH:mm:ss")}</span>
                                        </div>
                                        {job.started_at ? (
                                            <div className="flex justify-between text-sm">
                                                <span className="text-muted-foreground">Started:</span>
                                                <span>{format(new Date(job.started_at), "MMM d, yyyy HH:mm:ss")}</span>
                                            </div>
                                        ) : (
                                            <div className="flex justify-between text-sm">
                                                <span className="text-muted-foreground">Started:</span>
                                                <span className="text-gray-400">Not started yet</span>
                                            </div>
                                        )}
                                        {job.completed_at ? (
                                            <div className="flex justify-between text-sm">
                                                <span className="text-muted-foreground">Completed:</span>
                                                <span>{format(new Date(job.completed_at), "MMM d, yyyy HH:mm:ss")}</span>
                                            </div>
                                        ) : (
                                            <div className="flex justify-between text-sm">
                                                <span className="text-muted-foreground">Completed:</span>
                                                <span className="text-gray-400">Not completed yet</span>
                                            </div>
                                        )}
                                        {(job.started_at && job.completed_at) ? (
                                            <div className="flex justify-between text-sm">
                                                <span className="text-muted-foreground">Duration:</span>
                                                <span>
                                                    {formatDuration(new Date(job.started_at), new Date(job.completed_at))}
                                                </span>
                                            </div>
                                        ) : (
                                            <div className="flex justify-between text-sm">
                                                <span className="text-muted-foreground">Duration:</span>
                                                <span className="text-gray-400">N/A</span>
                                            </div>
                                        )}
                                    </div>
                                </div>
                            </Fragment>
                        )}
                    </CardContent>
                </Card>

                <LogViewer workflowId={workflowId} jobId={jobId} jobStatus={job?.status || "PENDING"} />
            </div>
        </div>
    )
}

// Helper function to format duration between two dates
function formatDuration(start: Date, end: Date): string {
    const diffInSeconds = Math.floor((end.getTime() - start.getTime()) / 1000);

    if (diffInSeconds < 60) {
        return `${diffInSeconds} second${diffInSeconds !== 1 ? 's' : ''}`;
    }

    const minutes = Math.floor(diffInSeconds / 60);
    const seconds = diffInSeconds % 60;

    if (minutes < 60) {
        return `${minutes} minute${minutes !== 1 ? 's' : ''} ${seconds} second${seconds !== 1 ? 's' : ''}`;
    }

    const hours = Math.floor(minutes / 60);
    const remainingMinutes = minutes % 60;

    return `${hours} hour${hours !== 1 ? 's' : ''} ${remainingMinutes} minute${remainingMinutes !== 1 ? 's' : ''}`;
}

// Helper function to get status badge config
function getStatusInfo(status: string) {
    switch (status) {
        case "COMPLETED":
            return {
                color: "bg-green-100 dark:bg-green-900/30 text-green-700 dark:text-green-400 border-green-200 dark:border-green-800/30",
                icon: <CheckCircle className="h-4 w-4" />
            }
        case "FAILED":
            return {
                color: "bg-red-100 dark:bg-red-900/30 text-red-700 dark:text-red-400 border-red-200 dark:border-red-800/30",
                icon: <XCircle className="h-4 w-4" />
            }
        case "QUEUED":
            return {
                color: "bg-yellow-100 dark:bg-yellow-900/30 text-yellow-700 dark:text-yellow-400 border-yellow-200 dark:border-yellow-800/30",
                icon: <Clock className="h-4 w-4" />
            }
        case "RUNNING":
            return {
                color: "bg-blue-100 dark:bg-blue-900/30 text-blue-700 dark:text-blue-400 border-blue-200 dark:border-blue-800/30",
                icon: <Loader2 className="h-4 w-4 animate-spin" />
            }
        default:
            return {
                color: "bg-gray-100 dark:bg-gray-900/30 text-gray-700 dark:text-gray-400 border-gray-200 dark:border-gray-800/30",
                icon: <AlertTriangle className="h-4 w-4" />
            }
    }
}