"use client"

import {
    Fragment,
    useCallback,
    useEffect,
    useRef,
    useState
} from "react"

import {
    Loader2,
    Play,
    Square,
    ArrowLeft
} from "lucide-react"

import { Button } from "@/components/ui/button"
import { Switch } from "@/components/ui/switch"
import { Badge } from "@/components/ui/badge"
import {
    Card,
    CardContent,
    CardHeader,
    CardTitle
} from "@/components/ui/card"

import {
    useTestWorkflow,
    TestWorkflowRunStatus
} from "@/hooks/use-test-workflow"
import { CreateWorkflowPayload } from "@/hooks/use-workflows"

import { cn, jsonRegex } from "@/lib/utils"
import {
    type StatusContext,
    getStatusLabel,
    getStatusMeta
} from "@/lib/status"

interface TestLogsViewerProps {
    workflowData: CreateWorkflowPayload
    onBackToCreate: () => void
}

const getLogStreamStyles = (stream: string) => {
    switch (stream) {
        case "stderr":
            return "text-muted-foreground bg-red-50 dark:bg-red-800/20"
        case "stdout":
        default:
            return ""
    }
}

const getStatusText = (status: TestWorkflowRunStatus): {
    status: string,
    context: StatusContext
} => {
    switch (status) {
        case TestWorkflowRunStatus.TEST_WORKFLOW_RUN_STATUS_BUILDING:
            return { status: "Building", context: "workflow" }
        case TestWorkflowRunStatus.TEST_WORKFLOW_RUN_STATUS_RUNNING:
            return { status: "Running", context: "job" }
        case TestWorkflowRunStatus.TEST_WORKFLOW_RUN_STATUS_COMPLETED:
            return { status: "Completed", context: "job" }
        case TestWorkflowRunStatus.TEST_WORKFLOW_RUN_STATUS_FAILED:
            return { status: "Failed", context: "job" }
        case TestWorkflowRunStatus.TEST_WORKFLOW_RUN_STATUS_CANCELED:
            return { status: "Canceled", context: "job" }
        default:
            return { status: "Unknown", context: "job" }
    }
}

export function TestLogsViewer({ workflowData, onBackToCreate }: TestLogsViewerProps) {
    const [parseJson, setParseJson] = useState<boolean>(true)
    const logContainerRef = useRef<HTMLDivElement>(null)

    const {
        isConnected,
        isRunning,
        status: _status,
        logs,
        error,
        startTest,
        stopTest
    } = useTestWorkflow(workflowData)

    useEffect(() => {
        if (logContainerRef.current && logs.length > 0) {
            const container = logContainerRef.current
            const isScrolledToBottom = container.scrollHeight - container.clientHeight <= container.scrollTop + 1

            if (isScrolledToBottom) {
                container.scrollTop = container.scrollHeight
            }
        }
    }, [logs.length])

    const parseLog = useCallback((message: string) => {
        if (!parseJson) return message

        try {
            // Try to parse the entire message as JSON
            const parsed = JSON.parse(message)
            return JSON.stringify(parsed, null, 2)
        } catch {
            // If that fails, try to find JSON objects within the message
            return message.replace(jsonRegex, (match) => {
                try {
                    const parsed = JSON.parse(match)
                    return JSON.stringify(parsed, null, 2)
                } catch {
                    return match
                }
            })
        }
    }, [parseJson])

    const handleStartTest = async () => {
        await startTest(workflowData)
    }

    const handleStopTest = () => {
        stopTest()
    }

    const handleBackToCreate = () => {
        if (isRunning) {
            stopTest()
        }
        onBackToCreate()
    }

    const status = getStatusText(_status)
    const statusMeta = getStatusMeta(status.status)
    const StatusIcon = statusMeta.icon

    return (
        <Card className="flex flex-1 flex-col w-full h-full p-0">
            <CardHeader className="sticky top-0 z-30 bg-card/95 backdrop-blur supports-[backdrop-filter]:bg-card/60 border-b p-4 rounded-t-2xl">
                <div className="flex sm:flex-row flex-col items-center justify-between gap-2">
                    <CardTitle className="flex flex-col items-center gap-0">
                        <div className="flex flex-row items-center">
                            <Button variant="ghost" size="sm" onClick={handleBackToCreate} className="p-1 h-8 w-8">
                                <ArrowLeft className="h-4 w-4" />
                            </Button>
                            Test Workflow: {workflowData.name}
                        </div>
                        <div
                            className={cn("flex items-center gap-2 text-sm", {
                                "text-green-600 dark:text-green-400": isConnected,
                                "text-red-600 dark:text-red-400": !isConnected,
                            })}
                        >
                            <div
                                className={cn("w-2 h-2 rounded-full", {
                                    "bg-green-500 animate-pulse": isConnected,
                                    "bg-red-500": !isConnected,
                                })}
                            />
                            {isConnected ? "Connected" : "Disconnected"}
                        </div>
                    </CardTitle>

                    <div className="flex items-center gap-2">
                        <Badge
                            variant="outline"
                            className={cn(
                                "px-2 py-0 h-5 font-medium flex items-center gap-1 border-none",
                                statusMeta.badgeClass
                            )}
                        >
                            <StatusIcon className={cn("h-3 w-3", statusMeta.iconClass)} />
                            <span className="text-xs">{getStatusLabel(status.status, status.context)}</span>
                        </Badge>

                        {!isRunning ? (
                            <Button
                                onClick={handleStartTest}
                                disabled={isRunning}
                                size="sm"
                                className="cursor-pointer"
                            >
                                <Play className="h-4 w-4 mr-2" />
                                Retest Workflow
                            </Button>
                        ) : (
                            <Button
                                onClick={handleStopTest}
                                disabled={!isRunning}
                                variant="destructive"
                                size="sm"
                                className="cursor-pointer"
                            >
                                <Square className="h-4 w-4 mr-2" />
                                Stop Test
                            </Button>
                        )}
                    </div>
                </div>

                <div className="flex items-center justify-end">
                    <div className="flex items-center gap-2">
                        <span className={cn("text-sm font-medium", { "text-muted-foreground": !parseJson })}>JSON</span>
                        <Switch checked={parseJson} onCheckedChange={setParseJson} disabled={logs.length === 0} />
                    </div>
                </div>
            </CardHeader>

            <CardContent ref={logContainerRef} className="w-full font-mono text-sm md:p-2 p-0 flex-1 overflow-y-auto">
                {error ? (
                    <div className="flex items-center justify-center h-full">
                        <div className="text-center">
                            <div className="text-red-500 mb-2">Test failed</div>
                            <div className="text-sm text-muted-foreground">{error}</div>
                        </div>
                    </div>
                ) : logs.length > 0 ? (
                    <Fragment>
                        {logs.map((log, index) => {
                            const formattedMessage = parseLog(log.message)
                            return (
                                <div
                                    key={index}
                                    className={cn(
                                        "flex hover:bg-muted/50 px-2 py-1 group",
                                        getLogStreamStyles(log.stream)
                                    )}
                                >
                                    <span className="text-muted-foreground mr-4 select-none min-w-[5ch] text-right hover:text-primary">
                                        {index + 1}
                                    </span>
                                    <span className="flex-1 whitespace-pre-wrap break-all">
                                        {formattedMessage}
                                    </span>
                                </div>
                            )
                        })}
                    </Fragment>
                ) : isRunning || _status === TestWorkflowRunStatus.TEST_WORKFLOW_RUN_STATUS_BUILDING ? (
                    <div className="flex items-center justify-center h-full">
                        <div className="flex items-center gap-2">
                            <Loader2 className="h-6 w-6 animate-spin" />
                            <span>
                                {_status === TestWorkflowRunStatus.TEST_WORKFLOW_RUN_STATUS_BUILDING
                                    ? "Building workflow..."
                                    : "Running test workflow..."}
                            </span>
                        </div>
                    </div>
                ) : (
                    <div className="flex flex-col items-center justify-center h-full text-muted-foreground">
                        <div className="text-lg mb-2">Test {status.status}</div>
                        <div className="text-sm text-center">No logs were produced during the test execution</div>
                    </div>
                )}
            </CardContent>
        </Card>
    )
}
