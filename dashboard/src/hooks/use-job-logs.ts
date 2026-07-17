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

import { useWorkflowDetails } from "@/hooks/use-workflow-details"

import { fetchWithAuth } from "@/lib/api-client"
import { buildLogViewerUrl } from "@/lib/log-line-selection"

const API_URL = process.env.NEXT_PUBLIC_API_URL

export type JobLog = {
    event_id?: string
    highlightToken?: string
    timestamp: string
    message: string
    sequence_num: number
    stream: "stdout" | "stderr"
}

type JobLogWire = {
    event_id?: string
    eventId?: string
    timestamp?: string
    message?: string
    sequence_num?: number | string
    sequenceNum?: number | string
    stream?: string
}

export type JobLogsResponseData = {
    id: string
    workflow_id: string
    logs: JobLog[]
    cursor?: string
    highlight_token?: string
}

export type DownloadLogsFormat = "txt" | "json" | "jsonl"

type DownloadLogsOptions = {
    filename: string
    format: DownloadLogsFormat
}

const kindsWithLogs = ['CONTAINER']
const terminalJobStatus = ['COMPLETED', 'FAILED', 'CANCELED']

const logKey = (log: JobLog): string => {
    if (log.event_id) {
        return log.event_id
    }

    return `${log.sequence_num}:${log.stream}:${log.timestamp}:${log.message}`
}

const sequenceNumFromEventID = (eventID?: string): number | undefined => {
    if (!eventID) {
        return undefined
    }

    const sequenceNum = Number(eventID.split(":").at(-1))
    return Number.isFinite(sequenceNum) ? sequenceNum : undefined
}

const normalizeJobLog = (log: JobLogWire, highlightToken?: string): JobLog => {
    const eventID = log.event_id || log.eventId
    const sequenceNum = Number(log.sequence_num ?? log.sequenceNum ?? sequenceNumFromEventID(eventID) ?? 0)

    return {
        event_id: eventID,
        highlightToken,
        timestamp: log.timestamp || "",
        message: log.message || "",
        sequence_num: Number.isFinite(sequenceNum) ? sequenceNum : 0,
        stream: log.stream === "stderr" ? "stderr" : "stdout",
    }
}

const mergeLiveLogs = (existingLogs: JobLog[], newLogs: JobLog[]): JobLog[] => {
    return sortLogsDesc(uniqueLogsByKey([...existingLogs, ...newLogs]))
}

const compareLogsDesc = (a: JobLog, b: JobLog): number => {
    const sequenceOrder = b.sequence_num - a.sequence_num
    if (sequenceOrder !== 0) return sequenceOrder

    const streamOrder = a.stream.localeCompare(b.stream)
    if (streamOrder !== 0) return streamOrder

    const eventOrder = (a.event_id || logKey(a)).localeCompare(b.event_id || logKey(b))
    if (eventOrder !== 0) return eventOrder

    return b.timestamp.localeCompare(a.timestamp)
}

const sortLogsDesc = (logs: JobLog[]): JobLog[] => {
    return [...logs].sort(compareLogsDesc)
}

const uniqueLogsByKey = (logs: JobLog[]): JobLog[] => {
    const uniqueLogsMap = new Map<string, JobLog>()

    logs.forEach((log) => {
        const key = logKey(log)
        const existingLog = uniqueLogsMap.get(key)
        if (!existingLog || compareLogsDesc(log, existingLog) < 0) {
            uniqueLogsMap.set(key, log)
        }
    })

    return Array.from(uniqueLogsMap.values())
}

const logsFromPages = (pages?: JobLogsResponseData[]): JobLog[] => {
    if (!pages?.length) {
        return []
    }

    return sortLogsDesc(uniqueLogsByKey(pages.flatMap((page) => page?.logs || [])))
}

const downloadFilename = (filename: string, format: DownloadLogsFormat) => {
    const base = (filename.trim() || "logs").replace(/\.(txt|json|jsonl)$/i, "")
    return `${base}.${format}`
}

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

    const logsURL = `${API_URL}/workflows/${workflowId}/jobs/${jobId}/logs`

    const sseURL = `${API_URL}/workflows/${workflowId}/jobs/${jobId}/events`
    const logsDownloadURL = `${API_URL}/workflows/${workflowId}/jobs/${jobId}/logs/raw`

    const searchURL = `${API_URL}/workflows/${workflowId}/jobs/${jobId}/logs/search`

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

            const response = await fetchWithAuth(`${logsDownloadURL}?${params.toString()}`)
            if (!response.ok) {
                throw new Error("failed to download logs")
            }

            const blob = await response.blob()
            const objectUrl = URL.createObjectURL(blob)
            const a = document.createElement("a")
            a.href = objectUrl
            a.download = downloadFilename(filename, format)
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
    const jobLogsInfiniteQueryKey = ["job-logs", workflowId, jobId, jobStatus]
    const jobLogsInfiniteQuery = useInfiniteQuery<JobLogsResponseData, Error>({
        queryKey: jobLogsInfiniteQueryKey,
        queryFn: async ({ pageParam }) => {
            const isFirstPage = !pageParam
            const url = pageParam
                ? `${logsURL}?cursor=${pageParam}`
                : logsURL

            const response = await fetchWithAuth(url, isFirstPage ? { cache: "no-store" } : undefined)

            if (!response.ok) {
                throw new Error("failed to fetch job logs")
            }

            const res = await response.json() as JobLogsResponseData

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
    const jobLogsSearchInfiniteQuery = useInfiniteQuery<JobLogsResponseData, Error>({
        queryKey: ["job-logs/search", workflowId, jobId, searchQuery, streamFilter],
        queryFn: async ({ pageParam }) => {
            const isFirstPage = !pageParam
            const url = pageParam
                ? `${searchURL}?${getSearchQueryParams}&cursor=${pageParam}`
                : `${searchURL}?${getSearchQueryParams}`

            const response = await fetchWithAuth(url, isFirstPage ? { cache: "no-store" } : undefined);

            if (!response.ok) {
                throw new Error("failed to fetch job logs");
            }

            const res = await response.json() as JobLogsResponseData

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
