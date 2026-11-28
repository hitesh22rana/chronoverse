"use client"

import {
    useEffect,
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
import { toast } from "sonner"

import { useWorkflowScope } from "@/features/workflows/workflow-freshness"
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
import { readLogDownload } from "@/features/logs/log-download"
import { buildLogViewerUrl } from "@/features/logs/log-line-selection"

type DownloadLogsOptions = {
    filename: string
    format: DownloadLogsFormat
}

const logDownloadLimitBytes = 8 * 1024 * 1024

const kindsWithLogs = ['CONTAINER']
const statusesWithLogs = ['RUNNING', 'COMPLETED', 'FAILED', 'CANCELED']

export function useJobLogs(routeWorkflowId: string, routeJobId: string, jobStatus: string) {
    const workflowId = routeWorkflowId.toLowerCase()
    const jobId = routeJobId.toLowerCase()
    const scope = useWorkflowScope(workflowId)
    const { workflow, isLoading: isWorkflowLoading } = useWorkflowDetails(workflowId)
    const { searchQuery, streamFilter, updateSearchQuery, applyStreamFilter, getSearchQueryParams } = useLogFilters()

    const [live, setLive] = useState<{ url: string, logs: JobLog[] } | null>(null)

    const logsURL = apiEndpoints.workflows.jobs.logs(workflowId, jobId)
    const sseURL = apiEndpoints.workflows.jobs.logEvents(workflowId, jobId)
    const logsDownloadURL = apiEndpoints.workflows.jobs.rawLogs(workflowId, jobId)
    const searchURL = apiEndpoints.workflows.jobs.searchLogs(workflowId, jobId)

    const isRunning = jobStatus === "RUNNING"
    const hasLogs = statusesWithLogs.includes(jobStatus)
    const workflowKind = workflow?.kind || ""
    const supportsLogs = kindsWithLogs.includes(workflowKind)
    const isLogsUnsupportedForKind = Boolean(workflow) && !supportsLogs
    const isRetentionDisabled = supportsLogs && !workflow.log_retention
    const shouldFetch = supportsLogs && !isRetentionDisabled && hasLogs

    const canFetch = scope.available && shouldFetch && Boolean(workflowId) && Boolean(jobId)

    const downloadLogsMutation = useMutation({
        mutationFn: async ({ filename, format }: DownloadLogsOptions) => {
            scope.requireCurrent()
            const signal = scope.signal()
            const response = await fetchApi(
                withQuery(logsDownloadURL, getSearchQueryParams),
                "failed to download logs",
                { signal },
            )

            const blob = await readLogDownload(response, {
                format,
                id: jobId,
                workflow_id: workflowId,
                filters: { q: searchQuery, stream: streamFilter || "all" },
            }, logDownloadLimitBytes)
            scope.requireCurrent()
            const objectUrl = URL.createObjectURL(blob)
            const a = document.createElement("a")
            a.href = objectUrl
            a.download = getDownloadFilename(filename, format)
            try {
                document.body.appendChild(a)
                a.click()
            } finally {
                a.remove()
                URL.revokeObjectURL(objectUrl)
            }

        },
        onSuccess: () => {
            if (scope.isCurrent()) toast.success("Logs downloaded successfully")
        },
        onError: (error) => {
            if (scope.isCurrent()) toast.error(error.message)
        }
    })

    // Retained logs are newest-first; fetching the next page loads older logs.
    const jobLogsInfiniteQuery = useInfiniteQuery<JobLogsResponse, Error>({
        queryKey: scope.queryKey(queryKeys.job.logs(workflowId, jobId, jobStatus)),
        queryFn: async ({ pageParam, signal }) => {
            scope.requireCurrent()
            const res = await fetchLogPage(logsURL, pageParam, signal)

            return retainedLogPage(res)
        },
        initialPageParam: null,
        getNextPageParam: (lastPage) => lastPage?.cursor || null,
        refetchOnMount: "always",
        enabled: canFetch && !getSearchQueryParams,
    })

    const jobLogsSearchInfiniteQuery = useInfiniteQuery<JobLogsResponse, Error>({
        queryKey: scope.queryKey(queryKeys.job.logSearch(workflowId, jobId, searchQuery, streamFilter)),
        queryFn: async ({ pageParam, signal }) => {
            scope.requireCurrent()
            const res = await fetchLogPage(searchURL, pageParam, signal, getSearchQueryParams)

            return searchedLogPage(res, workflowId, jobId)
        },
        initialPageParam: null,
        getNextPageParam: (lastPage) => lastPage?.cursor || null,
        refetchOnMount: "always",
        enabled: canFetch && Boolean(getSearchQueryParams),
    });

    const resourceSignal = scope.signal
    useEffect(() => {
        if (!canFetch || !isRunning || getSearchQueryParams) {
            return
        }

        const signal = resourceSignal()
        if (signal.aborted) return
        const close = subscribeToJobLogs(sseURL, (log) => {
            setLive((existing) => ({
                url: sseURL,
                logs: mergeLiveLogs(existing?.url === sseURL ? existing.logs : [], [log]),
            }))
        })
        const retire = () => {
            close()
            setLive(null)
        }
        signal.addEventListener("abort", retire, { once: true })
        return () => {
            signal.removeEventListener("abort", retire)
            close()
        }
    }, [sseURL, isRunning, canFetch, getSearchQueryParams, resourceSignal])

    useEffect(() => {
        if (!scope.available) return
        if (jobLogsSearchInfiniteQuery.error instanceof Error) {
            toast.error(jobLogsSearchInfiniteQuery.error.message)
        }
        if (jobLogsInfiniteQuery.error instanceof Error) {
            toast.error(jobLogsInfiniteQuery.error.message)
        }
    }, [jobLogsSearchInfiniteQuery.error, jobLogsInfiniteQuery.error, scope.available])

    const isSearching = shouldFetch && Boolean(getSearchQueryParams)
    const query = scope.available && hasLogs ? (isSearching ? jobLogsSearchInfiniteQuery : jobLogsInfiniteQuery) : {
        data: undefined,
        isLoading: false,
        error: null,
        fetchNextPage: () => Promise.resolve(),
        refetch: () => Promise.resolve(),
        isFetchingNextPage: false,
        hasNextPage: false,
    }
    const logs = logsFromPages(query.data?.pages)

    return {
        logs: isRunning && !isSearching && live?.url === sseURL && scope.available ? mergeLiveLogs(logs, live.logs) : logs,
        isLoading: query.isLoading,
        error: query.error,
        fetchNextPage: () => scope.isCurrent() ? query.fetchNextPage() : Promise.resolve(),
        canRefreshLogs: canFetch,
        refreshLogs: () => canFetch && scope.isCurrent() ? query.refetch() : undefined,
        isFetchingNextPage: query.isFetchingNextPage,
        hasNextPage: query.hasNextPage,
        searchQuery,
        updateSearchQuery: (value: string) => { if (scope.isCurrent()) updateSearchQuery(value) },
        streamFilter,
        applyStreamFilter: (value: string) => { if (scope.isCurrent()) applyStreamFilter(value) },
        downloadLogsMutation: {
            ...downloadLogsMutation,
            mutate: (options: DownloadLogsOptions, callbacks?: { onSuccess?: () => void }) => {
                if (!scope.isCurrent()) return
                downloadLogsMutation.mutate(options, {
                    onSuccess: () => { if (scope.isCurrent()) callbacks?.onSuccess?.() },
                })
            },
        },
        isDownloadLogsMutationLoading: downloadLogsMutation.isPending,
        isRetentionDisabled,
        isLogsUnsupportedForKind,
        workflowKind,
        isWorkflowLoading,
    }
}

function useLogFilters() {
    const pathname = usePathname()
    const router = useRouter()
    const searchParams = useSearchParams()

    const searchQuery = searchParams.get("q") || ""
    const streamFilter = searchParams.get("stream") || ""

    const updateFilter = (name: string, value: string) => {
        const params = new URLSearchParams(searchParams.toString())
        if (value) {
            params.set(name, value)
        } else {
            params.delete(name)
        }
        router.push(buildLogViewerUrl(pathname, params.toString(), ""))
    }

    const params = new URLSearchParams()
    if (searchQuery) params.set("q", searchQuery)
    if (streamFilter) params.set("stream", streamFilter)

    return {
        searchQuery,
        streamFilter,
        updateSearchQuery: (value: string) => updateFilter("q", value),
        applyStreamFilter: (value: string) => updateFilter("stream", value),
        getSearchQueryParams: params.toString(),
    }
}

function subscribeToJobLogs(sseURL: string, append: (_log: JobLog) => void) {
    let closed = false
    const eventSource = new EventSource(sseURL, {
        withCredentials: true,
    })

    const handleLog = (event: MessageEvent<string>) => {
        if (closed) return
        try {
            const logData = normalizeJobLog(JSON.parse(event.data) as JobLogWire)

            append(logData)
        } catch {
            /* ignore parsing errors */
        }
    }

    const handleError = () => {
        if (!closed) toast.error("Log streaming error occurred")
    }

    eventSource.addEventListener("log", handleLog)
    eventSource.addEventListener("error", handleError)

    eventSource.onerror = () => {
        // Silent on normal disconnects.
        if (!closed && eventSource.readyState !== EventSource.CLOSED) {
            toast.error("Lost connection to log stream")
        }
    }

    return () => {
        if (closed) return
        closed = true
        eventSource.onerror = null
        eventSource.removeEventListener("log", handleLog)
        eventSource.removeEventListener("error", handleError)
        eventSource.close()
    }
}

function fetchLogPage(url: string, pageParam: unknown, signal: AbortSignal, search = "") {
    const params = new URLSearchParams(search)
    if (pageParam) params.set("cursor", String(pageParam))
    return fetchApiJson<JobLogsResponse>(
        withQuery(url, params),
        "failed to fetch job logs",
        { signal },
    )
}

function retainedLogPage(res: JobLogsResponse): JobLogsResponse {
    return {
        id: res.id,
        workflow_id: res.workflow_id,
        logs: (res.logs || []).map((log) => normalizeJobLog(log)),
        cursor: res.cursor || undefined,
    }
}

function searchedLogPage(res: JobLogsResponse, workflowId: string, jobId: string): JobLogsResponse {
    return {
        id: jobId,
        workflow_id: workflowId,
        logs: (res.logs || []).map((log) => normalizeJobLog(log, res.highlight_token)),
        cursor: res.cursor || undefined,
        highlight_token: res.highlight_token,
    }
}
