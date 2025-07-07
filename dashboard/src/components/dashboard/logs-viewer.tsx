"use client"

import {
    useState,
    useEffect,
    useRef,
    useMemo,
    useCallback,
} from "react"
import {
    Loader2,
    Search,
    ChevronUp,
    ChevronDown,
    Download,
} from "lucide-react"

import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
    Card,
    CardContent,
    CardHeader,
    CardTitle,
} from "@/components/ui/card"

import { useJobLogs } from "@/hooks/use-job-logs"

import { cn } from "@/lib/utils"

interface LogViewerProps {
    workflowId: string
    jobId: string
    jobStatus: string
}

interface SearchMatch {
    lineIndex: number
    startIndex: number
    endIndex: number
}

// Determine styling based on log stream
const getLogStreamStyles = (stream: string) => {
    switch (stream) {
        case 'stderr':
            return 'text-muted-foreground bg-red-50 dark:bg-red-800/20'
        case 'stdout':
        default:
    }
}

export function LogsViewer({ workflowId, jobId, jobStatus }: LogViewerProps) {
    const [searchQuery, setSearchQuery] = useState("")
    const [currentMatchIndex, setCurrentMatchIndex] = useState(0)
    const [isSearchFocused, setIsSearchFocused] = useState(false)
    const logContainerRef = useRef<HTMLDivElement>(null)
    const searchInputRef = useRef<HTMLInputElement>(null)

    const {
        logs,
        isLoading: isLogsLoading,
        error: logsError,
        fetchNextPage,
        isFetchingNextPage,
        hasNextPage,
    } = useJobLogs(workflowId, jobId, jobStatus)

    // Infinite scroll handler
    const handleScroll = useCallback(() => {
        if (!logContainerRef.current || !hasNextPage || isFetchingNextPage) return

        const container = logContainerRef.current
        const { scrollTop, scrollHeight, clientHeight } = container
        const distanceFromBottom = scrollHeight - scrollTop - clientHeight

        // Trigger when user is near the bottom (within 200px)
        if (distanceFromBottom < 200) {
            fetchNextPage()
        }
    }, [hasNextPage, isFetchingNextPage, fetchNextPage])

    // Add scroll listener with throttling
    useEffect(() => {
        const container = logContainerRef.current
        if (!container) return

        // Add throttling to prevent too many calls
        let ticking = false
        const throttledHandleScroll = () => {
            if (!ticking) {
                requestAnimationFrame(() => {
                    handleScroll()
                    ticking = false
                })
                ticking = true
            }
        }

        container.addEventListener('scroll', throttledHandleScroll, { passive: true })

        // Check on mount if container is too small and needs more logs
        setTimeout(() => {
            if (container.scrollHeight <= container.clientHeight && hasNextPage && !isFetchingNextPage) {
                fetchNextPage()
            }
        }, 100)

        return () => {
            container.removeEventListener('scroll', throttledHandleScroll)
        }
    }, [handleScroll, hasNextPage, isFetchingNextPage, fetchNextPage])

    // Auto-load more logs when we have few entries
    useEffect(() => {
        if (logs && logs.length > 0 && logs.length < 50 && hasNextPage && !isFetchingNextPage && !isLogsLoading) {
            fetchNextPage()
        }
    }, [logs, hasNextPage, isFetchingNextPage, isLogsLoading, fetchNextPage])

    // Find all search matches
    const searchMatches = useMemo(() => {
        if (!searchQuery.trim() || !logs || logs.length === 0) return []

        const matches: SearchMatch[] = []
        const query = searchQuery.toLowerCase()

        logs.forEach((log, lineIndex) => {
            const logText = log.message.toLowerCase()
            let startIndex = 0

            while (true) {
                const foundIndex = logText.indexOf(query, startIndex)
                if (foundIndex === -1) break

                matches.push({
                    lineIndex,
                    startIndex: foundIndex,
                    endIndex: foundIndex + query.length,
                })

                startIndex = foundIndex + 1
            }
        })

        return matches
    }, [logs, searchQuery])

    // Reset current match when search changes
    useEffect(() => {
        setCurrentMatchIndex(0)
    }, [searchQuery])

    // Scroll to current match
    useEffect(() => {
        if (searchMatches.length > 0 && logContainerRef.current) {
            const currentMatch = searchMatches[currentMatchIndex]
            const lineElement = logContainerRef.current.children[currentMatch.lineIndex] as HTMLElement

            if (lineElement) {
                lineElement.scrollIntoView({
                    behavior: "smooth",
                    block: "center",
                })
            }
        }
    }, [currentMatchIndex, searchMatches])

    // Keyboard shortcuts
    useEffect(() => {
        const handleKeyDown = (e: KeyboardEvent) => {
            if ((e.ctrlKey || e.metaKey) && e.key === "f") {
                e.preventDefault()
                searchInputRef.current?.focus()
                setIsSearchFocused(true)
            }

            if (e.key === "Escape" && isSearchFocused) {
                setSearchQuery("")
                setIsSearchFocused(false)
                searchInputRef.current?.blur()
            }

            if (searchMatches.length > 0 && !isSearchFocused) {
                if (e.key === "F3" || (e.ctrlKey && e.key === "g")) {
                    e.preventDefault()
                    navigateToMatch("next")
                }

                if ((e.shiftKey && e.key === "F3") || (e.ctrlKey && e.shiftKey && e.key === "G")) {
                    e.preventDefault()
                    navigateToMatch("prev")
                }
            }
        }

        window.addEventListener("keydown", handleKeyDown)
        return () => window.removeEventListener("keydown", handleKeyDown)
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [searchMatches.length, isSearchFocused])

    const navigateToMatch = (direction: "next" | "prev") => {
        if (searchMatches.length === 0) return

        if (direction === "next") {
            setCurrentMatchIndex((prev) => (prev + 1) % searchMatches.length)
        } else {
            setCurrentMatchIndex((prev) => (prev - 1 + searchMatches.length) % searchMatches.length)
        }
    }

    const highlightText = (text: string, lineIndex: number) => {
        if (!searchQuery.trim()) return text

        const lineMatches = searchMatches.filter((match) => match.lineIndex === lineIndex)
        if (lineMatches.length === 0) return text

        const result = []
        let lastIndex = 0

        lineMatches.forEach((match, matchIndex) => {
            // Add text before match
            if (match.startIndex > lastIndex) {
                result.push(text.slice(lastIndex, match.startIndex))
            }

            // Add highlighted match
            const isCurrentMatch =
                searchMatches.findIndex((m) => m.lineIndex === lineIndex && m.startIndex === match.startIndex) ===
                currentMatchIndex

            result.push(
                <span
                    key={`match-${lineIndex}-${matchIndex}`}
                    className={cn(
                        "py-0.5 rounded-xs",
                        isCurrentMatch
                            ? "bg-orange-400 text-white"
                            : "bg-yellow-200 dark:bg-yellow-800 text-black dark:text-white"
                    )}
                >
                    {text.slice(match.startIndex, match.endIndex)}
                </span>
            )

            lastIndex = match.endIndex
        })

        // Add remaining text
        if (lastIndex < text.length) {
            result.push(text.slice(lastIndex))
        }

        return result
    }

    const downloadLogs = () => {
        if (!logs || logs.length === 0) return

        const logText = logs.map(log => log.message || '').join("\n")
        const blob = new Blob([logText], { type: "text/plain" })
        const url = URL.createObjectURL(blob)
        const a = document.createElement("a")
        a.href = url
        a.download = `job-${jobId}-logs.txt`
        a.click()
        URL.revokeObjectURL(url)
    }

    return (
        <Card className="flex flex-col flex-1 h-full">
            <CardHeader className="flex-shrink-0 space-y-4">
                <div className="flex items-center justify-between">
                    <CardTitle>
                        Logs
                    </CardTitle>
                    <Button
                        variant="outline"
                        size="sm"
                        onClick={downloadLogs}
                        disabled={logs.length === 0}
                    >
                        <Download className="h-4 w-4 mr-2" />
                        Download
                    </Button>
                </div>

                {/* Search Bar */}
                {logs.length > 0 && (
                    <div className="relative max-w-lg w-full flex items-center gap-2">
                        <Search className="absolute left-3 top-1/2 transform -translate-y-1/2 h-4 w-4 text-muted-foreground" />
                        <Input
                            ref={searchInputRef}
                            placeholder="Search logs... (Ctrl+F)"
                            value={searchQuery}
                            onChange={(e) => setSearchQuery(e.target.value)}
                            onFocus={() => setIsSearchFocused(true)}
                            onBlur={() => setIsSearchFocused(false)}
                            className="pl-10 pr-24 w-full"
                        />
                        {searchQuery && (
                            <div className="absolute right-0 flex items-center">
                                <span className="text-sm text-muted-foreground whitespace-nowrap">
                                    {searchMatches.length > 0
                                        ? `${currentMatchIndex + 1}/${searchMatches.length}`
                                        : '0/0'
                                    }
                                </span>

                                <div className="flex items-center gap-0.5 mx-2">
                                    <Button
                                        variant="ghost"
                                        size="sm"
                                        onClick={() => navigateToMatch("prev")}
                                        disabled={searchMatches.length === 0}
                                        className="h-4 w-4 p-0 rounded-none hover:bg-muted"
                                    >
                                        <ChevronUp className="h-4 w-4" />
                                    </Button>
                                    <Button
                                        variant="ghost"
                                        size="sm"
                                        onClick={() => navigateToMatch("next")}
                                        disabled={searchMatches.length === 0}
                                        className="h-4 w-4 p-0 rounded-none hover:bg-muted"
                                    >
                                        <ChevronDown className="h-4 w-4" />
                                    </Button>
                                </div>
                            </div>
                        )}
                    </div>
                )}
            </CardHeader>

            <CardContent className="flex-1 p-0 overflow-hidden">
                <div className="h-full w-full font-mono text-sm overflow-hidden">
                    {isLogsLoading ? (
                        <div className="flex items-center justify-center h-full">
                            <div className="flex items-center gap-2">
                                <Loader2 className="h-6 w-6 animate-spin" />
                                <span>Loading logs...</span>
                            </div>
                        </div>
                    ) : logsError ? (
                        <div className="flex items-center justify-center h-full">
                            <div className="text-center">
                                <div className="text-red-500 mb-2">Error loading logs</div>
                                <div className="text-sm text-muted-foreground">
                                    {logsError.message}
                                </div>
                            </div>
                        </div>
                    ) : logs.length > 0 ? (
                        <div ref={logContainerRef} className="h-full overflow-auto px-4 pt-4 scroll-smooth">
                            {logs.map((log, index) => {
                                return (
                                    <div
                                        key={index}
                                        className={cn(
                                            "flex hover:bg-muted/50 px-2 py-1 group",
                                            getLogStreamStyles(log.stream)
                                        )}
                                    >
                                        <span className="text-muted-foreground mr-4 select-none min-w-[4ch] text-right hover:text-primary">
                                            {index + 1}
                                        </span>
                                        <span className="flex-1 whitespace-pre-wrap break-all">
                                            {highlightText(log.message, index)}
                                        </span>
                                    </div>
                                )
                            })}

                            {/* Loading indicator for infinite scroll */}
                            {isFetchingNextPage && (
                                <div className="flex items-center justify-center py-4">
                                    <Loader2 className="h-4 w-4 animate-spin mr-2" />
                                    <span className="text-sm text-muted-foreground">Loading more logs...</span>
                                </div>
                            )}
                        </div>
                    ) : (
                        <div className="flex flex-col items-center justify-center h-full text-muted-foreground">
                            <div className="text-lg mb-2">No logs available</div>
                            <div className="text-sm text-center">
                                {jobStatus === 'RUNNING'
                                    ? 'Logs will appear here as the job executes'
                                    : (jobStatus === 'PENDING' || jobStatus === 'QUEUED')
                                        ? 'Job is waiting to start'
                                        : jobStatus === 'FAILED'
                                            ? 'Job failed to execute, no logs available'
                                            : jobStatus === 'COMPLETED'
                                                ? 'Job completed successfully, but no logs were produced'
                                                : 'This job did not produce any logs'
                                }
                            </div>
                        </div>
                    )}
                </div>
            </CardContent>
        </Card>
    )
}