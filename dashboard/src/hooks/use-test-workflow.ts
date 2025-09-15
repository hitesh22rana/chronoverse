"use client"

import {
    useEffect,
    useRef,
    useState
} from "react"
import { EventSourcePolyfill } from "event-source-polyfill";
import { toast } from "sonner"

import { type JobLog } from "@/hooks/use-job-logs"
import { type CreateWorkflowPayload } from "@/hooks/use-workflows"

export enum TestWorkflowRunStatus {
    TEST_WORKFLOW_RUN_STATUS_UNSPECIFIED = 0,
    TEST_WORKFLOW_RUN_STATUS_BUILDING = 1,
    TEST_WORKFLOW_RUN_STATUS_RUNNING = 2,
    TEST_WORKFLOW_RUN_STATUS_COMPLETED = 3,
    TEST_WORKFLOW_RUN_STATUS_FAILED = 4,
    TEST_WORKFLOW_RUN_STATUS_CANCELED = 5,
}

export type StreamTestWorkflowRunResponse = {
    status: TestWorkflowRunStatus
    Data: {
        Log?: JobLog
        Message?: string
    }
}

const API_URL = process.env.NEXT_PUBLIC_API_URL

export function useTestWorkflow(workflowData?: CreateWorkflowPayload) {
    const [isConnected, setIsConnected] = useState(false)
    const [isRunning, setIsRunning] = useState(false)
    const [status, setStatus] = useState<TestWorkflowRunStatus>(
        TestWorkflowRunStatus.TEST_WORKFLOW_RUN_STATUS_UNSPECIFIED,
    )
    const [logs, setLogs] = useState<JobLog[]>([])
    const [error, setError] = useState<string | null>(null)
    const eventSourceRef = useRef<unknown>(null)
    const sseURL = API_URL + "/workflows/test"

    const startTest = async (testWorkflowData: CreateWorkflowPayload) => {
        try {
            setError(null)
            setLogs([])
            setIsRunning(true)

            const fullURL = new URL(sseURL, window.location.origin)
            fullURL.searchParams.set("data", JSON.stringify(testWorkflowData))

            const eventSource = new EventSourcePolyfill(fullURL.toString(), {
                withCredentials: true,
            })

            eventSourceRef.current = eventSource
            setIsConnected(true)

            eventSource.addEventListener("open", () => {
                setIsConnected(true)
            })

            eventSource.addEventListener("log", (event) => {
                try {
                    const messageEvent = event as MessageEvent
                    const data: StreamTestWorkflowRunResponse = JSON.parse(messageEvent.data)

                    // Update status if provided
                    if (data.status) {
                        setStatus(data.status)
                    }

                    if (!data.Data?.Log) {
                        return
                    }

                    const logData: JobLog = data.Data?.Log
                    setLogs((prevLogs) => [...prevLogs, logData])
                } catch (error) {
                    console.error("Failed to parse log data:", error)
                }
            })

            eventSource.addEventListener("message", (event) => {
                try {
                    const messageEvent = event as MessageEvent
                    const data: StreamTestWorkflowRunResponse = JSON.parse(messageEvent.data)

                    // Update status if provided
                    if (data.status) {
                        setStatus(data.status)
                    }

                    // Handle workflow completion states
                    if (data.status === TestWorkflowRunStatus.TEST_WORKFLOW_RUN_STATUS_COMPLETED) {
                        setIsConnected(false)
                        setIsRunning(false)
                        toast.success("Test workflow completed successfully")
                        eventSource.close()
                    } else if (data.status === TestWorkflowRunStatus.TEST_WORKFLOW_RUN_STATUS_FAILED) {
                        setIsConnected(false)
                        setIsRunning(false)
                        setError("Test workflow failed")
                        toast.error("Test workflow failed")
                        eventSource.close()
                    } else if (data.status === TestWorkflowRunStatus.TEST_WORKFLOW_RUN_STATUS_CANCELED) {
                        setIsConnected(false)
                        setIsRunning(false)
                        toast.info("Test workflow was canceled")
                        eventSource.close()
                    }

                    if (!data.Data?.Message) {
                        return
                    }
                    toast.info(data.Data?.Message)
                } catch (error) {
                    console.error("Failed to parse message data:", error)
                }
            })

            eventSource.addEventListener("error", (event) => {
                try {
                    const messageEvent = event as MessageEvent
                    const data = JSON.parse(messageEvent.data)
                    if (data.message) {
                        toast.error(data.message)
                    } else {
                        toast.error("An error occurred during workflow execution")
                    }
                    // eslint-disable-next-line @typescript-eslint/no-unused-vars
                } catch (error) {
                    toast.error("Workflow execution error")
                }
            })

            eventSource.addEventListener("unknown", (event) => {
                try {
                    const messageEvent = event as MessageEvent
                    const data = JSON.parse(messageEvent.data)
                    if (data.message) {
                        toast.warning(data.message)
                    } else {
                        toast.warning("Unknown event received")
                    }
                    // eslint-disable-next-line @typescript-eslint/no-unused-vars
                } catch (error) {
                    toast.warning("Unknown event received")
                }
            })

            eventSource.addEventListener("end", () => {
                setIsConnected(false)
                setIsRunning(false)
                eventSource.close()
            })

            eventSource.onerror = () => {
                console.error("SSE connection error:", error)
                setError("SSE connection error")
                setIsConnected(false)
                setIsRunning(false)
                toast.error("Connection lost")
                eventSource.close()
            }
        } catch (error) {
            const errorMessage = error instanceof Error ? error.message : "Failed to start test workflow"
            setError(errorMessage)
            setIsRunning(false)
            setIsConnected(false)
            setStatus(TestWorkflowRunStatus.TEST_WORKFLOW_RUN_STATUS_FAILED)
            toast.error(errorMessage)
        }
    }

    const stopTest = () => {
        if (eventSourceRef.current) {
            (eventSourceRef.current as EventSourcePolyfill).close()
            eventSourceRef.current = null
        }
        setIsConnected(false)
        setIsRunning(false)
        setStatus(TestWorkflowRunStatus.TEST_WORKFLOW_RUN_STATUS_CANCELED)
        toast.info("Test workflow stopped")
    }

    useEffect(() => {
        if (workflowData && !isRunning) {
            startTest(workflowData)
        }
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [workflowData])

    useEffect(() => {
        return () => {
            if (eventSourceRef.current) {
                (eventSourceRef.current as EventSourcePolyfill).close()
                eventSourceRef.current = null
            }
        }
    }, [])

    return {
        isConnected,
        isRunning,
        status,
        logs,
        error,
        startTest,
        stopTest,
    }
}