"use client"

import {
    useCallback,
    useEffect,
    useMemo,
    useRef,
    useState,
} from "react"
import {
    useRouter,
    useSearchParams,
} from "next/navigation"
import {
    useInfiniteQuery,
    useMutation,
    useQuery,
    useQueryClient
} from "@tanstack/react-query"
import { EventSourcePolyfill } from "event-source-polyfill"
import { toast } from "sonner"

import { useWorkflowDetails } from "@/hooks/use-workflow-details"

import { fetchWithAuth } from "@/lib/api-client"

const API_URL = process.env.NEXT_PUBLIC_API_URL

export type JobLog = {
    timestamp: string
    message: string
    sequence_num: number
    stream: "stdout" | "stderr"
}

export type JobLogsResponseData = {
    id: string
    workflow_id: string
    logs: JobLog[]
    cursor?: string
}

const kindsWithLogs = ['CONTAINER']
const terminalJobStatus = ['COMPLETED', 'FAILED']

export function useJobLogs(workflowId: string, jobId: string, jobStatus: string) {
    const { workflow } = useWorkflowDetails(workflowId)
    const router = useRouter()
    const queryClient = useQueryClient()
    const searchParams = useSearchParams()

    const searchQuery = searchParams.get("q") || ""
    const streamFilter = searchParams.get("stream") || ""

    const [isConnected, setIsConnected] = useState(false)
    const [isSSEEnabled, setIsSSEEnabled] = useState(false)
    const eventSourceRef = useRef<EventSourcePolyfill | null>(null)

    const logsURL = `${API_URL}/workflows/${workflowId}/jobs/${jobId}/logs`

    const sseURL = `${API_URL}/workflows/${workflowId}/jobs/${jobId}/events`
    const logsDownloadURL = `${API_URL}/workflows/${workflowId}/jobs/${jobId}/logs/raw`

    const searchURL = `${API_URL}/workflows/${workflowId}/jobs/${jobId}/logs/search`

    const isRunning = jobStatus === "RUNNING"
    const isCompleted = terminalJobStatus.includes(jobStatus)
    const shouldFetch = !!workflow && kindsWithLogs.includes(workflow.kind) && (isCompleted || isRunning)

    // Update search query in URL params
    const updateSearchQuery = useCallback((newSearchQuery: string) => {
        const params = new URLSearchParams(searchParams.toString())

        if (newSearchQuery) {
            params.set("q", newSearchQuery)
        } else {
            params.delete("q")
        }

        router.push(`?${params.toString()}`)
    }, [router, searchParams])

    // Apply stream filter in URL params
    const applyStreamFilter = useCallback((newStreamFilter: string) => {
        const params = new URLSearchParams(searchParams.toString())

        if (newStreamFilter) {
            params.set("stream", newStreamFilter)
        } else {
            params.delete("stream")
        }

        router.push(`?${params.toString()}`)
    }, [router, searchParams])

    // Build query parameters for the search job logs request
    const getSearchQueryParams = useMemo(() => {
        const params = new URLSearchParams()

        if (searchQuery) {
            params.set("q", searchQuery)
        }

        if (streamFilter) {
            params.set("stream", streamFilter)
        }

        return params.toString()
    }, [searchQuery, streamFilter])

    // Download raw logs from backend and trigger browser file download
    const downloadLogsMutation = useMutation({
        mutationFn: async () => {
            const response = await fetchWithAuth(logsDownloadURL)
            if (!response.ok) {
                throw new Error("failed to download logs")
            }

            const blob = await response.blob()
            const objectUrl = URL.createObjectURL(blob)
            const a = document.createElement("a")
            a.href = objectUrl
            a.download = `${jobId}-logs.txt`
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

    // Helper function to merge logs and remove duplicates
    const mergeLogs = (existingLogs: JobLog[], newLogs: JobLog[]): JobLog[] => {
        const combined = [...existingLogs, ...newLogs]

        // Create a map to track unique logs by sequence_num
        const uniqueLogsMap = new Map<number, JobLog>()

        // Add all logs to the map, later entries will overwrite earlier ones with same sequence_num
        combined.forEach(log => {
            uniqueLogsMap.set(log.sequence_num, log)
        })

        // Convert back to array and sort by sequence_num
        return Array.from(uniqueLogsMap.values()).sort((a, b) => a.sequence_num - b.sequence_num)
    }

    // For completed jobs, use infinite query with pagination
    const jobLogsInfiniteQuery = useInfiniteQuery<JobLogsResponseData, Error>({
        queryKey: ["job-logs", workflowId, jobId, jobStatus],
        queryFn: async ({ pageParam }) => {
            const url = pageParam
                ? `${logsURL}?cursor=${pageParam}`
                : logsURL

            const response = await fetchWithAuth(url)

            if (!response.ok) {
                throw new Error("failed to fetch job logs")
            }

            const res = await response.json() as JobLogsResponseData

            return {
                id: res.id,
                workflow_id: res.workflow_id,
                logs: res.logs || [],
                cursor: res.cursor || undefined,
            }
        },
        initialPageParam: null,
        getNextPageParam: (lastPage) => lastPage?.cursor || null,
        enabled: shouldFetch && !getSearchQueryParams && !!workflowId && !!jobId,
    })

    // Search job logs query
    const jobLogsSearchInfiniteQuery = useInfiniteQuery<JobLogsResponseData, Error>({
        queryKey: ["job-logs/search", workflowId, jobId, searchQuery, streamFilter],
        queryFn: async ({ pageParam }) => {
            const url = pageParam
                ? `${searchURL}?${getSearchQueryParams}&cursor=${pageParam}`
                : `${searchURL}?${getSearchQueryParams}`

            const response = await fetchWithAuth(url);

            if (!response.ok) {
                throw new Error("failed to fetch job logs");
            }

            const res = await response.json() as JobLogsResponseData

            return {
                id: jobId,
                workflow_id: workflowId,
                logs: res.logs || [],
                cursor: res.cursor || undefined,
            }
        },
        initialPageParam: null,
        getNextPageParam: (lastPage) => lastPage?.cursor || null,
        enabled: shouldFetch && !!getSearchQueryParams && !!workflowId && !!jobId,
    });

    // For running jobs, use regular query + SSE
    const jobLogsSSEQueryKey = useMemo(() => [`job-logs/events`, workflowId, jobId], [workflowId, jobId])
    const jobLogsSSEQuery = useQuery({
        queryKey: jobLogsSSEQueryKey,
        queryFn: async (): Promise<JobLog[]> => {
            const allLogs: JobLog[] = []
            let cursor: string | undefined = undefined

            // Fetch all pages of logs
            while (true) {
                const url = cursor
                    ? `${logsURL}?cursor=${cursor}`
                    : logsURL

                const response = await fetchWithAuth(url)

                if (!response.ok) {
                    throw new Error("Failed to fetch job logs")
                }

                const res = await response.json() as JobLogsResponseData

                if (res.logs && res.logs.length > 0) {
                    allLogs.push(...res.logs)
                }

                // If there's no cursor, we've fetched all pages
                if (!res.cursor) {
                    break
                }

                cursor = res.cursor
            }

            return allLogs
        },
        enabled: shouldFetch && !getSearchQueryParams && isRunning && !!workflowId && !!jobId,
        staleTime: Infinity,
        gcTime: Infinity,
    })

    // Handle SSE connection for running jobs
    useEffect(() => {
        // Only connect to SSE if:
        // 1. Job is running
        // 2. Initial logs have been fetched successfully
        // 3. We have workflowId and jobId
        if (!isRunning || !jobLogsSSEQuery.isSuccess || !workflowId || !jobId) {
            setIsConnected(false)
            setIsSSEEnabled(false)
            return
        }

        let firstPass: boolean = true;

        // Enable SSE after initial fetch is complete
        setIsSSEEnabled(true)

        const eventSource = new EventSourcePolyfill(sseURL, {
            withCredentials: true,
        })

        eventSourceRef.current = eventSource

        eventSource.onopen = () => {
            setIsConnected(true)
        }

        // Handle specific event types
        eventSource.addEventListener('log', (event) => {
            try {
                const messageEvent = event as MessageEvent
                const logData: JobLog = JSON.parse(messageEvent.data)

                queryClient.setQueryData(jobLogsSSEQueryKey, (oldData: JobLog[] | undefined) => {
                    const existingLogs = oldData || []
                    if (!firstPass) {
                        return [...existingLogs, logData]
                    }
                    firstPass = false;
                    return mergeLogs(existingLogs, [logData])
                })
            } catch { /* ignore parsing errors */ }
        })

        eventSource.addEventListener('error', () => {
            toast.error('Log streaming error occurred')
        })

        eventSource.addEventListener('end', () => {
            setIsConnected(false)
        })

        eventSource.onerror = () => {
            setIsConnected(false)

            // No error toast for normal disconnections
            if (eventSource.readyState !== EventSource.CLOSED) {
                toast.error('Lost connection to log stream')
            }
        }

        return () => {
            eventSource.close()
            eventSourceRef.current = null
            setIsConnected(false)
        }
    }, [queryClient, jobLogsSSEQueryKey, sseURL, isRunning, jobLogsSSEQuery.isSuccess, workflowId, jobId])

    useEffect(() => {
        if (jobLogsSearchInfiniteQuery.error instanceof Error) {
            toast.error(jobLogsSearchInfiniteQuery.error.message)
        }
        if (jobLogsInfiniteQuery.error instanceof Error) {
            toast.error(jobLogsInfiniteQuery.error.message)
        }
        if (jobLogsSSEQuery.error instanceof Error) {
            toast.error(jobLogsSSEQuery.error.message)
        }
    }, [jobLogsSearchInfiniteQuery.error, jobLogsInfiniteQuery.error, jobLogsSSEQuery.error])

    if (shouldFetch && !!getSearchQueryParams) {
        const allPages = jobLogsSearchInfiniteQuery.data?.pages || [];
        const logs = allPages.length > 0 ? allPages.flatMap((page) => page?.logs || []) : [];

        return {
            logs,
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
        };
    }

    if (isCompleted) {
        const allPages = jobLogsInfiniteQuery.data?.pages || []
        const logs = allPages.length > 0 ? allPages.flatMap((page) => page?.logs || []) : []

        return {
            logs,
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
        }
    }

    if (isRunning) {
        return {
            logs: jobLogsSSEQuery.data || [],
            isLoading: jobLogsSSEQuery.isLoading,
            error: jobLogsSSEQuery.error,
            fetchNextPage: () => Promise.resolve(),
            isFetchingNextPage: false,
            hasNextPage: false,
            refetch: jobLogsSSEQuery.refetch,
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
                    setIsSSEEnabled(false)
                }
            },
            downloadLogsMutation,
            isDownloadLogsMutationLoading: downloadLogsMutation.isPending,
            isDownloadLogsMutationError: downloadLogsMutation.error,
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
    }
}