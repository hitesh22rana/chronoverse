"use client"

import {
    useCallback,
    useEffect,
    useRef,
    useState,
    useTransition,
} from "react"
import {
    usePathname,
    useRouter,
    useSearchParams,
} from "next/navigation"
import {
    Download,
    Filter,
    Loader2,
    Search,
} from "lucide-react"
import { Virtuoso } from "react-virtuoso"

import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Switch } from "@/components/ui/switch"
import {
    Card,
    CardContent,
    CardHeader,
    CardTitle,
} from "@/components/ui/card"
import {
    Popover,
    PopoverTrigger,
    PopoverContent,
} from "@/components/ui/popover"
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group"
import { Label } from "@/components/ui/label"
import { Separator } from "@/components/ui/separator"

import { useJobLogs } from "@/hooks/use-job-logs"

import { cn, jsonRegex } from "@/lib/utils"

interface LogViewerProps {
    workflowId: string
    jobId: string
    jobStatus: string
    completedAt: string
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

const getLogStreamStripStyles = (stream: string) => {
    switch (stream) {
        case "stderr":
            return "bg-red-500 dark:bg-red-400"
        case "stdout":
        default:
            return "bg-emerald-500 dark:bg-emerald-400"
    }
}

const highlightClass = "rounded bg-orange-400/80 px-0.5 text-inherit dark:bg-orange-500/70"

const escapeHtml = (value: string) => {
    return value.replace(/[&<>"']/g, (char) => {
        switch (char) {
            case "&":
                return "&amp;"
            case "<":
                return "&lt;"
            case ">":
                return "&gt;"
            case "\"":
                return "&quot;"
            case "'":
                return "&#39;"
            default:
                return char
        }
    })
}

const escapeRegExp = (value: string) => {
    return value.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")
}

const stripHighlightTags = (message: string) => {
    return message.replace(/<\/?em>/g, "")
}

const extractHighlightedTerms = (message: string) => {
    const terms: string[] = []
    const highlightPattern = /<em>([\s\S]*?)<\/em>/g

    for (const match of message.matchAll(highlightPattern)) {
        const term = match[1]
        if (term?.trim()) {
            terms.push(term)
        }
    }

    return Array.from(new Set(terms)).sort((a, b) => b.length - a.length)
}

const renderHighlightedText = (message: string, terms: string[]) => {
    if (!terms.length) {
        return escapeHtml(message)
    }

    const highlightPattern = new RegExp(terms.map(escapeRegExp).join("|"), "gi")
    let lastIndex = 0
    let rendered = ""

    for (const match of message.matchAll(highlightPattern)) {
        const matchText = match[0]
        const matchIndex = match.index ?? 0

        rendered += escapeHtml(message.slice(lastIndex, matchIndex))
        rendered += `<mark class="${highlightClass}">${escapeHtml(matchText)}</mark>`
        lastIndex = matchIndex + matchText.length
    }

    rendered += escapeHtml(message.slice(lastIndex))
    return rendered
}

export function LogsViewer({
    workflowId,
    jobId,
    jobStatus,
    completedAt,
}: LogViewerProps) {
    const [isSearchPending, startSearchTransition] = useTransition()
    const router = useRouter()
    const pathname = usePathname()
    const searchParams = useSearchParams()

    const [popoverOpen, setPopoverOpen] = useState(false)
    const [isSearchFocused, setIsSearchFocused] = useState(false)
    const searchInputRef = useRef<HTMLInputElement>(null)

    const {
        logs,
        isLoading: isLogsLoading,
        error: logsError,
        fetchNextPage,
        searchQuery,
        updateSearchQuery,
        streamFilter,
        applyStreamFilter,
        isFetchingNextPage,
        hasNextPage,
        downloadLogsMutation,
        isDownloadLogsMutationLoading,
        isRetentionDisabled,
        isLogsUnsupportedForKind,
        workflowKind,
        isWorkflowLoading,
    } = useJobLogs(workflowId, jobId, jobStatus)

    const [searchInput, setSearchInput] = useState(searchQuery)
    const [stream, setStream] = useState(streamFilter || "all")
    const disableLogInteractions = isRetentionDisabled || isLogsUnsupportedForKind
    const parseJson = searchParams.get("json") === "true"

    const updateJsonRendering = useCallback((enabled: boolean) => {
        const params = new URLSearchParams(searchParams.toString())

        if (enabled) {
            params.set("json", "true")
        } else {
            params.delete("json")
        }

        const query = params.toString()
        router.replace(query ? `${pathname}?${query}` : pathname, { scroll: false })
    }, [pathname, router, searchParams])

    // Debounced search update
    useEffect(() => {
        const timer = setTimeout(() => {
            if (searchInput !== searchQuery) {
                startSearchTransition(() => {
                    updateSearchQuery(searchInput)
                })
            }
        }, 500)

        return () => clearTimeout(timer)
    }, [searchInput, searchQuery, updateSearchQuery])

    // Keyboard shortcuts
    useEffect(() => {
        const handleKeyDown = (e: KeyboardEvent) => {
            if ((e.ctrlKey || e.metaKey) && e.key === "f") {
                e.preventDefault()
                searchInputRef.current?.focus()
                setIsSearchFocused(true)
            }

            if (e.key === "Escape" && isSearchFocused) {
                setIsSearchFocused(false)
                searchInputRef.current?.blur()
            }
        }

        window.addEventListener("keydown", handleKeyDown)
        return () => window.removeEventListener("keydown", handleKeyDown)
    }, [isSearchFocused])

    // Parse log message and render Meili <em> highlights safely.
    const parseLog = useCallback(
        (message: string) => {
            const shouldRenderSearchHighlights = Boolean(searchQuery)
            const highlightedTerms = shouldRenderSearchHighlights ? extractHighlightedTerms(message) : []
            const rawMessage = shouldRenderSearchHighlights ? stripHighlightTags(message) : message

            if (!parseJson) {
                return renderHighlightedText(rawMessage, highlightedTerms)
            }

            try {
                const parsed = JSON.parse(rawMessage)
                return renderHighlightedText(JSON.stringify(parsed, null, 2), highlightedTerms)
            } catch {
                const formattedMessage = rawMessage.replace(jsonRegex, (match) => {
                    try {
                        const parsed = JSON.parse(match)
                        return JSON.stringify(parsed, null, 2)
                    } catch {
                        return match
                    }
                })

                return renderHighlightedText(formattedMessage, highlightedTerms)
            }
        },
        [parseJson, searchQuery]
    )

    // Row renderer for Virtuoso
    const LogRow = (index: number) => {
        const log = logs[index]
        if (!log) return null
        const formattedMessage = parseLog(log.message)
        return (
            <div
                className={cn(
                    "flex min-h-6 hover:bg-muted/50 group",
                    getLogStreamStyles(log.stream)
                )}
            >
                <span
                    className={cn("my-1 w-1 flex-none rounded-sm", getLogStreamStripStyles(log.stream))}
                    title={log.stream}
                    aria-hidden="true"
                />
                <span
                    className="flex-1 whitespace-pre-wrap break-all px-3 py-1"
                    dangerouslySetInnerHTML={{
                        __html: formattedMessage,
                    }}
                />
            </div>
        )
    }

    // Trigger pagination only when user reaches bottom of the Virtuoso viewport
    const handleEndReached = () => {
        if (hasNextPage && !isFetchingNextPage) {
            fetchNextPage()
        }
    }

    return (
        <Card className="flex flex-col flex-1 w-full min-h-dvh h-full">
            {/* Header sticks to top of page */}
            <CardHeader className="sticky top-0 z-30 bg-card/95 backdrop-blur supports-backdrop-filter:bg-card/60 border-b space-y-4 p-6 rounded-t-2xl">
                <CardTitle>Logs</CardTitle>

                <div className="flex lg:flex-row flex-col items-center justify-between gap-4">
                    {/* Search Bar */}
                    <div className="relative w-full flex items-center gap-2">
                        <Search className="absolute left-3 top-1/2 transform -translate-y-1/2 h-4 w-4 text-muted-foreground" />
                        <Input
                            ref={searchInputRef}
                            placeholder="Search logs... (Ctrl+F)"
                            value={searchInput}
                            onChange={(e) => setSearchInput(e.target.value)}
                            onFocus={() => setIsSearchFocused(true)}
                            onBlur={() => setIsSearchFocused(false)}
                            className="pl-10 pr-8 w-full"
                            disabled={disableLogInteractions}
                        />
                        <div className="absolute right-14 flex items-center">
                            {isSearchPending && <Loader2 className="size-4 animate-spin" />}
                        </div>
                        <div className="flex items-center gap-2">
                            <Popover open={popoverOpen} onOpenChange={setPopoverOpen}>
                                <PopoverTrigger asChild>
                                    <Button variant="outline" size="icon" aria-label="Filter logs" disabled={disableLogInteractions}>
                                        <Filter className="size-4" />
                                    </Button>
                                </PopoverTrigger>
                                <PopoverContent className="w-46 m-2 flex flex-col gap-4" align="end">
                                    <div className="flex flex-col gap-2">
                                        <Label className="text-md">Log level</Label>
                                        <Separator />
                                    </div>
                                    <RadioGroup
                                        value={stream}
                                        onValueChange={(value) => {
                                            setStream(value)
                                            setPopoverOpen(false)
                                            if (value !== (streamFilter || "all")) {
                                                startSearchTransition(() => {
                                                    applyStreamFilter(value === "all" ? "" : value)
                                                })
                                            }
                                        }}
                                        className="flex flex-col gap-2"
                                    >
                                        <div className="flex items-center gap-2">
                                            <RadioGroupItem value="all" id="stream-all" />
                                            <Label htmlFor="stream-all" className="text-sm font-medium">
                                                All
                                            </Label>
                                        </div>
                                        <div className="flex items-center gap-2">
                                            <RadioGroupItem value="stdout" id="stream-stdout" />
                                            <Label htmlFor="stream-stdout" className="text-sm font-medium">
                                                stdout
                                            </Label>
                                        </div>
                                        <div className="flex items-center gap-2">
                                            <RadioGroupItem value="stderr" id="stream-stderr" />
                                            <Label htmlFor="stream-stderr" className="text-sm font-medium">
                                                stderr
                                            </Label>
                                        </div>
                                    </RadioGroup>
                                </PopoverContent>
                            </Popover>
                        </div>
                    </div>

                    {/* Logs options */}
                    <div className="flex items-center gap-2">
                        <div className="flex items-center gap-2">
                            <span className={cn("text-sm font-medium", { "text-muted-foreground": !parseJson })}>
                                JSON
                            </span>
                            <Switch
                                checked={parseJson}
                                onCheckedChange={updateJsonRendering}
                                disabled={(jobStatus === "CANCELED" && logs.length === 0) || (logs.length === 0 && !!completedAt)}
                            />
                        </div>
                        <Button
                            variant="outline"
                            size="sm"
                            onClick={() => downloadLogsMutation.mutate()}
                            disabled={disableLogInteractions || isDownloadLogsMutationLoading || logs.length === 0 || jobStatus === "RUNNING"}
                        >
                            {isDownloadLogsMutationLoading ? (
                                <Loader2 className="mr-2 h-4 w-4 animate-spin" />
                            ) : (
                                <Download className="h-4 w-4 mr-2" />
                            )}
                            Download
                        </Button>
                    </div>
                </div>
            </CardHeader>

            <CardContent className="flex flex-col flex-1 w-full h-full font-mono text-sm md:p-2 p-0">
                {isLogsLoading || !jobStatus || isWorkflowLoading ? (
                    <div className="flex items-center justify-center h-full m-auto">
                        <div className="flex items-center gap-2">
                            <Loader2 className="h-6 w-6 animate-spin" />
                            <span>Loading logs...</span>
                        </div>
                    </div>
                ) : logsError ? (
                    <div className="flex items-center justify-center h-full m-auto">
                        <div className="text-center">
                            <div className="text-red-500 mb-2">Error loading logs</div>
                            <div className="text-sm text-muted-foreground">{logsError.message}</div>
                        </div>
                    </div>
                ) : logs.length > 0 ? (
                    <Virtuoso
                        totalCount={logs.length}
                        itemContent={LogRow}
                        endReached={handleEndReached}
                        overscan={200}
                        className="flex flex-1 w-full h-full"
                    />
                ) : isLogsUnsupportedForKind ? (
                    <div className="flex flex-col items-center justify-center h-full text-muted-foreground m-auto">
                        <div className="text-lg mb-2">No logs available</div>
                        <div className="text-sm text-center">
                            Logs are not available for <span className="dark:text-white text-black font-semibold">{workflowKind?.charAt(0) + workflowKind?.substring(1).toLowerCase()}</span> workflows.
                        </div>
                    </div>
                ) : isRetentionDisabled ? (
                    <div className="flex flex-col items-center justify-center h-full text-muted-foreground m-auto">
                        <div className="text-lg mb-2">No logs available</div>
                        <div className="text-sm text-center">Log retention is disabled for this <span className="dark:text-white text-black font-semibold">{workflowKind?.charAt(0) + workflowKind?.substring(1).toLowerCase()}</span> workflow</div>
                    </div>
                ) : (!!searchQuery || !!streamFilter) ? (
                    <div className="flex flex-col items-center justify-center h-full text-muted-foreground m-auto">
                        <div className="text-lg mb-2">No logs found</div>
                        <div className="text-sm text-center">Try adjusting your search query or filters</div>
                    </div>
                ) : (
                    <div className="flex flex-col items-center justify-center h-full text-muted-foreground m-auto">
                        <div className="text-lg mb-2">No logs available</div>
                        <div className="text-sm text-center">
                            {jobStatus === "RUNNING"
                                ? "Logs will appear here as the job executes"
                                : jobStatus === "PENDING" || jobStatus === "QUEUED"
                                    ? "Job is waiting to start"
                                    : jobStatus === "FAILED"
                                        ? "Job failed to execute, no logs available"
                                        : jobStatus === "COMPLETED"
                                            ? "Job completed successfully, but no logs were produced"
                                            : "This job did not produce any logs"}
                        </div>
                    </div>
                )}
                {isFetchingNextPage && (
                    <div className="flex items-center justify-center py-4">
                        <Loader2 className="h-4 w-4 animate-spin mr-2" />
                        <span className="text-sm text-muted-foreground">Loading older logs...</span>
                    </div>
                )}
            </CardContent>
        </Card>
    )
}
