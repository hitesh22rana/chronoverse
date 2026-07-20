"use client"

import {
    useEffect,
    useRef,
    useState,
} from "react"
import {
    usePathname,
    useRouter,
    useSearchParams,
} from "next/navigation"
import {
    useInfiniteQuery,
    useMutation,
} from "@tanstack/react-query"
import {
    EventSourcePolyfill,
    type Event as EventSourceEvent,
    type MessageEvent as EventSourceMessageEvent,
} from "event-source-polyfill"
import { toast } from "sonner"

import { useWorkflowDetails } from "@/features/workflows/use-workflow-details"

import { fetchApi, fetchApiJson } from "@/lib/api/client"
import { apiEndpoints, withQuery } from "@/lib/api/endpoints"
import { queryKeys } from "@/lib/api/query-keys"
import type {
    DownloadLogsFormat,
    JobLog,
    JobLogsResponse,
} from "@/features/logs/types"
import {
    getDownloadFilename,
    logsFromPages,
    mergeLiveLogs,
    normalizeJobLog,
    type JobLogWire,
} from "@/features/logs/log-data"
import { buildLogViewerUrl } from "@/features/logs/log-line-selection"

type DownloadLogsOptions = {
    filename: string
    format: DownloadLogsFormat
}

const kindsWithLogs = ['CONTAINER']
const terminalJobStatus = ['COMPLETED', 'FAILED', 'CANCELED']

export function useJobLogs(workflowId: string, jobId: string, jobStatus: string) {
    const { workflow, isLoading: isWorkflowLoading } = useWorkflowDetails(workflowId)
    const pathname = usePathname()
    const router = useRouter()
    const searchParams = useSearchParams()

    const searchQuery = searchParams.get("q") || ""
    const streamFilter = searchParams.get("stream") || ""

    const [isConnected, setIsConnected] = useState(false)
    const [liveLogs, setLiveLogs] = useState<JobLog[]>([])
    const eventSourceRef = useRef<EventSourcePolyfill | null>(null)

    const logsURL = apiEndpoints.workflows.jobs.logs(workflowId, jobId)
    const sseURL = apiEndpoints.workflows.jobs.logEvents(workflowId, jobId)
    const logsDownloadURL = apiEndpoints.workflows.jobs.rawLogs(workflowId, jobId)
    const searchURL = apiEndpoints.workflows.jobs.searchLogs(workflowId, jobId)

    const isRunning = jobStatus === "RUNNING"
    const isCompleted = terminalJobStatus.includes(jobStatus)
    const workflowKind = workflow?.kind || ""
    const isLogsUnsupportedForKind = Boolean(workflow && !kindsWithLogs.includes(workflow.kind))
    const isRetentionDisabled = Boolean(workflow && !isLogsUnsupportedForKind && !workflow.log_retention)
    const shouldFetch = Boolean(
        workflow &&
        !isLogsUnsupportedForKind &&
        workflow.log_retention &&
        (isCompleted || isRunning)
    )

    // Update search query in URL params
    const updateSearchQuery = (newSearchQuery: string) => {
        const params = new URLSearchParams(searchParams.toString())

        if (newSearchQuery) {
            params.set("q", newSearchQuery)
        } else {
            params.delete("q")
        }

        const query = params.toString()
        router.push(buildLogViewerUrl(pathname, query, ""))
    }

    // Apply stream filter in URL params
    const applyStreamFilter = (newStreamFilter: string) => {
        const params = new URLSearchParams(searchParams.toString())

        if (newStreamFilter) {
            params.set("stream", newStreamFilter)
        } else {
            params.delete("stream")
        }

        const query = params.toString()
        router.push(buildLogViewerUrl(pathname, query, ""))
    }

    // Build query parameters for the search job logs request
    const getSearchQueryParams = (() => {
        const params = new URLSearchParams()

        if (searchQuery) {
            params.set("q", searchQuery)
        }

        if (streamFilter) {
            params.set("stream", streamFilter)
        }

        return params.toString()
    })()

    // Download raw logs from backend and trigger browser file download
    const downloadLogsMutation = useMutation({
        mutationFn: async ({ filename, format }: DownloadLogsOptions) => {
            const params = new URLSearchParams(getSearchQueryParams)
            params.set("format", format)

            const response = await fetchApi(
                withQuery(logsDownloadURL, params),
                "failed to download logs",
            )

            const blob = await response.blob()
            const objectUrl = URL.createObjectURL(blob)
            const a = document.createElement("a")
            a.href = objectUrl
            a.download = getDownloadFilename(filename, format)
            document.body.appendChild(a)
            a.click()
            a.remove()
            URL.revokeObjectURL(objectUrl)

        },
        onSuccess: () => {
            toast.success("Logs downloaded successfully")
        },
        onError: (error) => {
            toast.error(error.message)
        }
    })

    // Retained logs are newest-first; fetching the next page loads older logs.
    const jobLogsInfiniteQuery = useInfiniteQuery<JobLogsResponse, Error>({
        queryKey: queryKeys.job.logs(workflowId, jobId, jobStatus),
        queryFn: async ({ pageParam }) => {
            const isFirstPage = !pageParam
            const params = new URLSearchParams()
            if (pageParam) {
                params.set("cursor", String(pageParam))
            }

            const res = await fetchApiJson<JobLogsResponse>(
                withQuery(logsURL, params),
                "failed to fetch job logs",
                isFirstPage ? { cache: "no-store" } : {},
            )

            return {
                id: res.id,
                workflow_id: res.workflow_id,
                logs: (res.logs || []).map((log) => normalizeJobLog(log)),
                cursor: res.cursor || undefined,
            }
        },
        initialPageParam: null,
        getNextPageParam: (lastPage) => lastPage?.cursor || null,
        refetchOnMount: "always",
        enabled: shouldFetch && !getSearchQueryParams && Boolean(workflowId) && Boolean(jobId),
    })

    // Search job logs query
    const jobLogsSearchInfiniteQuery = useInfiniteQuery<JobLogsResponse, Error>({
        queryKey: queryKeys.job.logSearch(workflowId, jobId, searchQuery, streamFilter),
        queryFn: async ({ pageParam }) => {
            const isFirstPage = !pageParam
            const params = new URLSearchParams(getSearchQueryParams)
            if (pageParam) {
                params.set("cursor", String(pageParam))
            }

            const res = await fetchApiJson<JobLogsResponse>(
                withQuery(searchURL, params),
                "failed to fetch job logs",
                isFirstPage ? { cache: "no-store" } : {},
            )

            return {
                id: jobId,
                workflow_id: workflowId,
                logs: (res.logs || []).map((log) => normalizeJobLog(log, res.highlight_token)),
                cursor: res.cursor || undefined,
                highlight_token: res.highlight_token,
            }
        },
        initialPageParam: null,
        getNextPageParam: (lastPage) => lastPage?.cursor || null,
        refetchOnMount: "always",
        enabled: shouldFetch && Boolean(getSearchQueryParams) && Boolean(workflowId) && Boolean(jobId),
    });

    // Handle SSE connection for running jobs
    useEffect(() => {
        if (!shouldFetch || !isRunning || Boolean(getSearchQueryParams) || !workflowId || !jobId) {
            return
        }

        const eventSource = new EventSourcePolyfill(sseURL, {
            withCredentials: true,
        })

        eventSourceRef.current = eventSource

        eventSource.onopen = () => {
            setIsConnected(true)
        }

        const handleLog = (event: EventSourceEvent) => {
            try {
                const messageEvent = event as EventSourceMessageEvent
                const logData = normalizeJobLog(JSON.parse(messageEvent.data) as JobLogWire)

                setLiveLogs((existingLogs) => mergeLiveLogs(existingLogs, [logData]))
            } catch { /* ignore parsing errors */ }
        }

        const handleError = () => {
            toast.error('Log streaming error occurred')
        }

        const handleEnd = () => {
            setIsConnected(false)
        }

        eventSource.addEventListener('log', handleLog)
        eventSource.addEventListener('error', handleError)
        eventSource.addEventListener('end', handleEnd)

        eventSource.onerror = () => {
            setIsConnected(false)

            // No error toast for normal disconnections
            if (eventSource.readyState !== EventSource.CLOSED) {
                toast.error('Lost connection to log stream')
            }
        }

        return () => {
            eventSource.removeEventListener('log', handleLog)
            eventSource.removeEventListener('error', handleError)
            eventSource.removeEventListener('end', handleEnd)
            eventSource.close()
            eventSourceRef.current = null
            setIsConnected(false)
        }
    }, [sseURL, isRunning, shouldFetch, getSearchQueryParams, workflowId, jobId])

    useEffect(() => {
        if (jobLogsSearchInfiniteQuery.error instanceof Error) {
            toast.error(jobLogsSearchInfiniteQuery.error.message)
        }
        if (jobLogsInfiniteQuery.error instanceof Error) {
            toast.error(jobLogsInfiniteQuery.error.message)
        }
    }, [jobLogsSearchInfiniteQuery.error, jobLogsInfiniteQuery.error])

    const retainedLogs = logsFromPages(jobLogsInfiniteQuery.data?.pages)
    const runningLogs = mergeLiveLogs(retainedLogs, liveLogs)
    const searchLogs = logsFromPages(jobLogsSearchInfiniteQuery.data?.pages)

    if (shouldFetch && Boolean(getSearchQueryParams)) {
        return {
            logs: searchLogs,
            isLoading: jobLogsSearchInfiniteQuery.isLoading,
            error: jobLogsSearchInfiniteQuery.error,
            fetchNextPage: jobLogsSearchInfiniteQuery.fetchNextPage,
            isFetchingNextPage: jobLogsSearchInfiniteQuery.isFetchingNextPage,
            hasNextPage: jobLogsSearchInfiniteQuery.hasNextPage,
            refetch: jobLogsSearchInfiniteQuery.refetch,
            searchQuery: searchQuery,
            updateSearchQuery: updateSearchQuery,
            streamFilter: streamFilter,
            applyStreamFilter: applyStreamFilter,
            downloadLogsMutation,
            isDownloadLogsMutationLoading: downloadLogsMutation.isPending,
            isDownloadLogsMutationError: downloadLogsMutation.error,
            isRetentionDisabled,
            isLogsUnsupportedForKind,
            workflowKind,
            isWorkflowLoading,
        };
    }

    if (isCompleted) {
        return {
            logs: retainedLogs,
            isLoading: jobLogsInfiniteQuery.isLoading,
            error: jobLogsInfiniteQuery.error,
            fetchNextPage: jobLogsInfiniteQuery.fetchNextPage,
            isFetchingNextPage: jobLogsInfiniteQuery.isFetchingNextPage,
            hasNextPage: jobLogsInfiniteQuery.hasNextPage,
            refetch: jobLogsInfiniteQuery.refetch,
            searchQuery: searchQuery,
            updateSearchQuery: updateSearchQuery,
            streamFilter: streamFilter,
            applyStreamFilter: applyStreamFilter,
            isConnected: false,
            isSSEEnabled: false,
            disconnect: () => { },
            getMaxSequenceNum: () => 0,
            downloadLogsMutation,
            isDownloadLogsMutationLoading: downloadLogsMutation.isPending,
            isDownloadLogsMutationError: downloadLogsMutation.error,
            isRetentionDisabled,
            isLogsUnsupportedForKind,
            workflowKind,
            isWorkflowLoading,
        }
    }

    if (isRunning) {
        const isSSEEnabled = shouldFetch && !getSearchQueryParams

        return {
            logs: runningLogs,
            isLoading: jobLogsInfiniteQuery.isLoading,
            error: jobLogsInfiniteQuery.error,
            fetchNextPage: jobLogsInfiniteQuery.fetchNextPage,
            isFetchingNextPage: jobLogsInfiniteQuery.isFetchingNextPage,
            hasNextPage: jobLogsInfiniteQuery.hasNextPage,
            refetch: jobLogsInfiniteQuery.refetch,
            searchQuery: searchQuery,
            updateSearchQuery: updateSearchQuery,
            streamFilter: streamFilter,
            applyStreamFilter: applyStreamFilter,
            isConnected,
            isSSEEnabled,
            disconnect: () => {
                if (eventSourceRef.current) {
                    eventSourceRef.current.close()
                    eventSourceRef.current = null
                    setIsConnected(false)
                }
            },
            downloadLogsMutation,
            isDownloadLogsMutationLoading: downloadLogsMutation.isPending,
            isDownloadLogsMutationError: downloadLogsMutation.error,
            isRetentionDisabled,
            isLogsUnsupportedForKind,
            workflowKind,
            isWorkflowLoading,
        }
    }

    // For PENDING and QUEUED - return empty state
    return {
        logs: [],
        isLoading: false,
        error: null,
        fetchNextPage: () => Promise.resolve(),
        isFetchingNextPage: false,
        hasNextPage: false,
        refetch: () => Promise.resolve(),
        searchQuery: searchQuery,
        updateSearchQuery: updateSearchQuery,
        streamFilter: streamFilter,
        applyStreamFilter: applyStreamFilter,
        isConnected: false,
        isSSEEnabled: false,
        disconnect: () => { },
        getMaxSequenceNum: () => 0,
        downloadLogsMutation,
        isDownloadLogsMutationLoading: downloadLogsMutation.isPending,
        isDownloadLogsMutationError: downloadLogsMutation.error,
        isRetentionDisabled,
        isLogsUnsupportedForKind,
        workflowKind,
        isWorkflowLoading,
    }
}
