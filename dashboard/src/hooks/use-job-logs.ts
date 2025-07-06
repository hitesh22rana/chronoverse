"use client"

import {
    useEffect,
    useMemo,
    useRef,
    useState
} from "react"
import {
    useInfiniteQuery,
    useQuery,
    useQueryClient
} from "@tanstack/react-query"
import { EventSourcePolyfill } from "event-source-polyfill"
import { toast } from "sonner"

import { fetchWithAuth } from "@/lib/api-client"
import { cleanLog } from "@/lib/utils"

const API_URL = process.env.NEXT_PUBLIC_API_URL

type JobLog = {
    timestamp: string
    message: string
    sequence_num: number
}

export type JobLogsResponseData = {
    id: string
    workflow_id: string
    logs: JobLog[]
    cursor?: string
}

export type JobLogsResponse = {
    id: string
    workflow_id: string
    logs: string[]
    cursor?: string
}

export function useJobLogs(workflowId: string, jobId: string, jobStatus: string) {
    const queryClient = useQueryClient()
    const [isConnected, setIsConnected] = useState(false)
    const [isSSEEnabled, setIsSSEEnabled] = useState(false)
    const eventSourceRef = useRef<EventSourcePolyfill | null>(null)

    const logsURL = `${API_URL}/workflows/${workflowId}/jobs/${jobId}/logs`
    const sseURL = `${API_URL}/workflows/${workflowId}/jobs/${jobId}/events`
    const isRunning = jobStatus === "RUNNING"
    const isCompleted = ["COMPLETED", "FAILED", "CANCELED"].includes(jobStatus)
    const shouldFetch = jobStatus !== "PENDING" && jobStatus !== "QUEUED"

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
    const infiniteQuery = useInfiniteQuery<JobLogsResponse, Error>({
        queryKey: ["job", workflowId, jobId, jobStatus],
        queryFn: async ({ pageParam }) => {
            const url = pageParam
                ? `${logsURL}?cursor=${pageParam}`
                : logsURL

            const response = await fetchWithAuth(url)

            if (!response.ok) {
                throw new Error("failed to fetch job logs")
            }

            const res = await response.json() as JobLogsResponseData

            if (!res.logs) {
                return {
                    id: jobId,
                    workflow_id: workflowId,
                    logs: [],
                    cursor: undefined,
                }
            }

            const data: JobLogsResponse = {
                id: res.id,
                workflow_id: res.workflow_id,
                logs: cleanLog(res.logs),
                cursor: res.cursor || undefined,
            }

            return data
        },
        initialPageParam: null,
        getNextPageParam: (lastPage) => lastPage?.cursor || null,
        enabled: shouldFetch && isCompleted,
    })

    // For running jobs, use regular query + SSE
    const sseQueryKey = useMemo(() => [`job/events`, workflowId, jobId], [workflowId, jobId])
    const sseQuery = useQuery({
        queryKey: sseQueryKey,
        queryFn: async (): Promise<JobLog[]> => {
            try {
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

                // Sort all logs by sequence_num to ensure proper order
                return allLogs.sort((a, b) => a.sequence_num - b.sequence_num)
            } catch (error) {
                throw error
            }
        },
        enabled: shouldFetch && isRunning,
        staleTime: Infinity,
        gcTime: Infinity,
    })

    // Handle SSE connection for running jobs
    useEffect(() => {
        // Only connect to SSE if:
        // 1. Job is running
        // 2. Initial logs have been fetched successfully
        // 3. We have workflowId and jobId
        if (!isRunning || !sseQuery.isSuccess || !workflowId || !jobId) {
            setIsConnected(false)
            setIsSSEEnabled(false)
            return
        }

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

                queryClient.setQueryData(sseQueryKey, (oldData: JobLog[] | undefined) => {
                    const existingLogs = oldData || []
                    return mergeLogs(existingLogs, [logData])
                })
                // eslint-disable-next-line @typescript-eslint/no-unused-vars
            } catch (_) {
            }
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
    }, [queryClient, sseQueryKey, sseURL, isRunning, sseQuery.isSuccess, workflowId, jobId])

    useEffect(() => {
        if (infiniteQuery.error instanceof Error) {
            toast.error(infiniteQuery.error.message)
        }
        if (sseQuery.error instanceof Error) {
            toast.error(sseQuery.error.message)
        }
    }, [infiniteQuery.error, sseQuery.error])

    if (isCompleted) {
        const allPages = infiniteQuery.data?.pages || []
        const logs = allPages.length > 0 ? allPages.flatMap((page) => page?.logs || []) : []

        return {
            logs,
            isLoading: infiniteQuery.isLoading,
            error: infiniteQuery.error,
            fetchNextPage: infiniteQuery.fetchNextPage,
            isFetchingNextPage: infiniteQuery.isFetchingNextPage,
            hasNextPage: infiniteQuery.hasNextPage,
            refetch: infiniteQuery.refetch,
            // SSE-specific properties (not available for completed jobs)
            isConnected: false,
            isSSEEnabled: false,
            disconnect: () => { },
            getMaxSequenceNum: () => 0,
        }
    }

    if (isRunning) {
        const rawLogs = sseQuery.data || []
        const logs = cleanLog(rawLogs)

        return {
            logs,
            isLoading: sseQuery.isLoading,
            error: sseQuery.error,
            fetchNextPage: () => Promise.resolve(),
            isFetchingNextPage: false,
            hasNextPage: false,
            refetch: sseQuery.refetch,
            // SSE-specific properties
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
            getMaxSequenceNum: () => {
                const logs = sseQuery.data || []
                return logs.length > 0 ? Math.max(...logs.map(log => log.sequence_num)) : 0
            },
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
        isConnected: false,
        isSSEEnabled: false,
        disconnect: () => { },
        getMaxSequenceNum: () => 0,
    }
}