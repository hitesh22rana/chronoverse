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
import { Switch } from "@/components/ui/switch"
import {
    Card,
    CardContent,
    CardHeader,
    CardTitle,
} from "@/components/ui/card"

import { useJobLogs } from "@/hooks/use-job-logs"

import { cn } from "@/lib/utils"
import { Trie } from "@/lib/trie"

interface LogViewerProps {
    workflowId: string
    jobId: string
    jobStatus: string
    completedAt: string
}

const jsonRegex = /\{[^{}]*(?:\{[^{}]*\}[^{}]*)*\}/g

const sevenDaysAgo = new Date((new Date()).getTime() - 7 * 24 * 60 * 60 * 1000)

function isPurgedLogs(completedAt: string) {
    return new Date(completedAt) < sevenDaysAgo
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

export function LogsViewer({ workflowId, jobId, jobStatus, completedAt }: LogViewerProps) {
    const [searchQuery, setSearchQuery] = useState("")
    const [debouncedSearchQuery, setDebouncedSearchQuery] = useState("")
    const [currentMatchIndex, setCurrentMatchIndex] = useState(0)
    const [isSearchFocused, setIsSearchFocused] = useState(false)
    const [parseJson, setParseJson] = useState(false)
    const logContainerRef = useRef<HTMLDivElement>(null)
    const searchInputRef = useRef<HTMLInputElement>(null)

    const trieRef = useRef<Trie>(new Trie())
    const processedLogsCountRef = useRef<number>(0)
    const currentJobRef = useRef<string>(jobId)

    const {
        logs,
        isLoading: isLogsLoading,
        error: logsError,
        fetchNextPage,
        isFetchingNextPage,
        hasNextPage,
    } = useJobLogs(workflowId, jobId, jobStatus)

    // Debounce search query to avoid excessive re-renders
    useEffect(() => {
        const timer = setTimeout(() => {
            setDebouncedSearchQuery(searchQuery)
        }, 150) // 150ms debounce

        return () => clearTimeout(timer)
    }, [searchQuery])

    // Clear search when input is empty
    useEffect(() => {
        if (!searchQuery.trim()) {
            setDebouncedSearchQuery("")
        }
    }, [searchQuery])

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

    const formatJsonLog = useCallback((message: string) => {
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

    useEffect(() => {
        // Reset trie if job changed or parseJson state changed
        if (currentJobRef.current !== jobId) {
            trieRef.current = new Trie()
            processedLogsCountRef.current = 0
            currentJobRef.current = jobId
        }

        if (!logs || logs.length === 0) {
            // Reset trie and counter when no logs
            trieRef.current = new Trie()
            processedLogsCountRef.current = 0
            return
        }

        // Rebuild trie when parseJson changes to use formatted content for search
        trieRef.current = new Trie()

        logs.forEach((log, index) => {
            if (log.message) {
                const searchableMessage = formatJsonLog(log.message)
                trieRef.current.insert(searchableMessage, index)
            }
        })

        // Update the count of processed logs
        processedLogsCountRef.current = logs.length
    }, [logs, jobId, parseJson, formatJsonLog])

    // Find all search matches using the Trie
    const searchMatches = useMemo(() => {
        if (!debouncedSearchQuery.trim()) return []

        // Only search if we have logs
        if (!logs || logs.length === 0) return []

        const matches = trieRef.current.search(debouncedSearchQuery)

        return matches.sort((a: { lineIndex: number; startIndex: number; endIndex: number }, b: { lineIndex: number; startIndex: number; endIndex: number }) => {
            if (a.lineIndex !== b.lineIndex) {
                return a.lineIndex - b.lineIndex
            }
            return a.startIndex - b.startIndex
        })
    }, [debouncedSearchQuery, logs]) // Include logs to invalidate when new logs are added

    // Reset current match when search changes
    useEffect(() => {
        setCurrentMatchIndex(0)
    }, [debouncedSearchQuery])

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
        if (!debouncedSearchQuery.trim()) return text

        const lineMatches = searchMatches.filter((match: { lineIndex: number; startIndex: number; endIndex: number }) => match.lineIndex === lineIndex)
        if (lineMatches.length === 0) return text

        const result = []
        let lastIndex = 0

        lineMatches.forEach((match: { lineIndex: number; startIndex: number; endIndex: number }, matchIndex: number) => {
            // Add text before match
            if (match.startIndex > lastIndex) {
                result.push(text.slice(lastIndex, match.startIndex))
            }

            // Add highlighted match
            const isCurrentMatch =
                searchMatches.findIndex((m: { lineIndex: number; startIndex: number; endIndex: number }) => m.lineIndex === lineIndex && m.startIndex === match.startIndex) ===
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
                    <div className="flex items-center gap-2">
                        <div className="flex items-center gap-2">
                            <span className={cn("text-sm font-medium", { "text-muted-foreground": !parseJson })}>JSON</span>
                            <Switch
                                checked={parseJson}
                                onCheckedChange={setParseJson}
                                disabled={
                                    (jobStatus === "CANCELED" && logs.length === 0) ||
                                    (logs.length === 0 && !!completedAt) || isPurgedLogs(completedAt)
                                }
                            />
                        </div>
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
                                const formattedMessage = formatJsonLog(log.message)
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
                                            {highlightText(formattedMessage, index)}
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
                                            : (jobStatus === 'COMPLETED' && logs.length === 0 && isPurgedLogs(completedAt))
                                                ? 'Logs are older than 7 days and have been purged'
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
