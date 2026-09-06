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
const statusesWithLogs = ['RUNNING', 'COMPLETED', 'FAILED', 'CANCELED']

export function useJobLogs(workflowId: string, jobId: string, jobStatus: string) {
    const { workflow, isLoading: isWorkflowLoading } = useWorkflowDetails(workflowId)
    const { searchQuery, streamFilter, updateSearchQuery, applyStreamFilter, getSearchQueryParams } = useLogFilters()

    const [liveLogs, setLiveLogs] = useState<JobLog[]>([])

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

    const canFetch = shouldFetch && Boolean(workflowId) && Boolean(jobId)

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
        enabled: canFetch && !getSearchQueryParams,
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
        enabled: canFetch && Boolean(getSearchQueryParams),
    });

    // Handle SSE connection for running jobs
    useEffect(() => {
        if (!shouldFetch || !isRunning || Boolean(getSearchQueryParams) || !workflowId || !jobId) {
            return
        }

        const eventSource = new EventSource(sseURL, {
            withCredentials: true,
        })

        const handleLog = (event: MessageEvent<string>) => {
            try {
                const logData = normalizeJobLog(JSON.parse(event.data) as JobLogWire)

                setLiveLogs((existingLogs) => mergeLiveLogs(existingLogs, [logData]))
            } catch { /* ignore parsing errors */ }
        }

        const handleError = () => {
            toast.error('Log streaming error occurred')
        }

        eventSource.addEventListener('log', handleLog)
        eventSource.addEventListener('error', handleError)

        eventSource.onerror = () => {
            // No error toast for normal disconnections
            if (eventSource.readyState !== EventSource.CLOSED) {
                toast.error('Lost connection to log stream')
            }
        }

        return () => {
            eventSource.removeEventListener('log', handleLog)
            eventSource.removeEventListener('error', handleError)
            eventSource.close()
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

    const isSearching = shouldFetch && Boolean(getSearchQueryParams)
    const query = hasLogs ? (isSearching ? jobLogsSearchInfiniteQuery : jobLogsInfiniteQuery) : {
        data: undefined,
        isLoading: false,
        error: null,
        fetchNextPage: () => Promise.resolve(),
        isFetchingNextPage: false,
        hasNextPage: false,
    }
    const logs = logsFromPages(query.data?.pages)

    return {
        logs: isRunning && !isSearching ? mergeLiveLogs(logs, liveLogs) : logs,
        isLoading: query.isLoading,
        error: query.error,
        fetchNextPage: query.fetchNextPage,
        isFetchingNextPage: query.isFetchingNextPage,
        hasNextPage: query.hasNextPage,
        searchQuery,
        updateSearchQuery,
        streamFilter,
        applyStreamFilter,
        downloadLogsMutation,
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
