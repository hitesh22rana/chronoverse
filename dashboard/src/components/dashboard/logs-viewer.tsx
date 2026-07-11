"use client"

import {
    useCallback,
    useEffect,
    useRef,
    useState,
    useTransition,
} from "react"
import type { MouseEvent as ReactMouseEvent } from "react"
import {
    usePathname,
    useRouter,
    useSearchParams,
} from "next/navigation"
import {
    Copy,
    Download,
    Ellipsis,
    Filter,
    Link,
    Loader2,
    Search,
} from "lucide-react"
import { toast } from "sonner"
import { Virtuoso } from "react-virtuoso"
import type { VirtuosoHandle } from "react-virtuoso"

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
import {
    DropdownMenu,
    DropdownMenuContent,
    DropdownMenuGroup,
    DropdownMenuItem,
    DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import {
    Tooltip,
    TooltipContent,
    TooltipTrigger,
} from "@/components/ui/tooltip"

import { useJobLogs } from "@/hooks/use-job-logs"
import type { DownloadLogsFormat } from "@/hooks/use-job-logs"

import { cn, jsonRegex } from "@/lib/utils"
import {
    buildLogViewerUrl,
    formatLogLineSelection,
    getSelectedLogText,
    getUnavailableSelectionMessage,
    isLogLineSelected,
    normalizeLogLineSelection,
    parseLogLineSelection,
    shouldFetchMoreLogsForSelection,
} from "@/lib/log-line-selection"
import type { LogLineSelection } from "@/lib/log-line-selection"

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
const highlightTokenPattern = /^[a-f0-9]{32}$/
const highlightStartPrefix = "__CV_HL_START_"
const highlightEndPrefix = "__CV_HL_END_"
const highlightSuffix = "__"
const shareableJobStatuses = new Set(["COMPLETED", "FAILED", "CANCELED"])

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

const renderHighlightedText = (message: string, highlightToken?: string) => {
    if (!highlightToken || !highlightTokenPattern.test(highlightToken)) {
        return escapeHtml(message)
    }

    const startTag = `${highlightStartPrefix}${highlightToken}${highlightSuffix}`
    const endTag = `${highlightEndPrefix}${highlightToken}${highlightSuffix}`
    let currentIndex = 0
    let rendered = ""

    while (currentIndex < message.length) {
        const startIndex = message.indexOf(startTag, currentIndex)
        if (startIndex === -1) {
            break
        }

        const matchStartIndex = startIndex + startTag.length
        const endIndex = message.indexOf(endTag, matchStartIndex)
        if (endIndex === -1) {
            break
        }

        rendered += escapeHtml(message.slice(currentIndex, startIndex))
        rendered += `<mark class="${highlightClass}">${escapeHtml(message.slice(matchStartIndex, endIndex))}</mark>`
        currentIndex = endIndex + endTag.length
    }

    rendered += escapeHtml(message.slice(currentIndex))
    return rendered
}

const getHighlightTags = (highlightToken?: string) => {
    if (!highlightToken || !highlightTokenPattern.test(highlightToken)) {
        return null
    }

    return {
        startTag: `${highlightStartPrefix}${highlightToken}${highlightSuffix}`,
        endTag: `${highlightEndPrefix}${highlightToken}${highlightSuffix}`,
    }
}

const parseHighlightedMessage = (message: string, highlightToken?: string) => {
    const tags = getHighlightTags(highlightToken)
    if (!tags) {
        return { rawMessage: message, highlightedSegments: [] }
    }

    const highlightedSegments: string[] = []
    let currentIndex = 0
    let rawMessage = ""

    while (currentIndex < message.length) {
        const startIndex = message.indexOf(tags.startTag, currentIndex)
        if (startIndex === -1) {
            break
        }

        const matchStartIndex = startIndex + tags.startTag.length
        const endIndex = message.indexOf(tags.endTag, matchStartIndex)
        if (endIndex === -1) {
            break
        }

        rawMessage += message.slice(currentIndex, startIndex)
        rawMessage += message.slice(matchStartIndex, endIndex)
        highlightedSegments.push(message.slice(matchStartIndex, endIndex))
        currentIndex = endIndex + tags.endTag.length
    }

    rawMessage += message.slice(currentIndex)
    return { rawMessage, highlightedSegments }
}

const renderHighlightedSegments = (message: string, segments: string[]) => {
    if (!segments.length) {
        return escapeHtml(message)
    }

    let currentIndex = 0
    let rendered = ""

    for (const segment of segments) {
        if (!segment) {
            continue
        }

        const segmentIndex = message.indexOf(segment, currentIndex)
        if (segmentIndex === -1) {
            continue
        }

        rendered += escapeHtml(message.slice(currentIndex, segmentIndex))
        rendered += `<mark class="${highlightClass}">${escapeHtml(segment)}</mark>`
        currentIndex = segmentIndex + segment.length
    }

    rendered += escapeHtml(message.slice(currentIndex))
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
    const [downloadPopoverOpen, setDownloadPopoverOpen] = useState(false)
    const [isSearchFocused, setIsSearchFocused] = useState(false)
    const searchInputRef = useRef<HTMLInputElement>(null)
    const pendingSearchQueryRef = useRef<string | null>(null)
    const virtuosoRef = useRef<VirtuosoHandle>(null)
    const deepLinkFetchInFlightRef = useRef(false)
    const lastScrolledSelectionRef = useRef("")
    const lastUnavailableSelectionRef = useRef("")
    const [lineSelection, setLineSelection] = useState<LogLineSelection | null>(null)
    const [selectionAnchor, setSelectionAnchor] = useState<number | null>(null)
    const [isSelectionUnavailable, setIsSelectionUnavailable] = useState(false)

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
    const [downloadFilename, setDownloadFilename] = useState(`${jobId}-logs`)
    const [downloadFormat, setDownloadFormat] = useState<DownloadLogsFormat>("txt")
    const disableLogInteractions = isRetentionDisabled || isLogsUnsupportedForKind
    const parseJson = searchParams.get("json") === "true"
    const datasetKey = `${pathname}?q=${searchQuery}&stream=${streamFilter}`
    const selectionFragment = lineSelection ? formatLogLineSelection(lineSelection) : ""
    const selectionKey = `${datasetKey}${selectionFragment}`

    const updateJsonRendering = useCallback((enabled: boolean) => {
        const params = new URLSearchParams(searchParams.toString())

        if (enabled) {
            params.set("json", "true")
        } else {
            params.delete("json")
        }

        const query = params.toString()
        const hash = window.location.hash
        router.replace(buildLogViewerUrl(pathname, query, hash), { scroll: false })
    }, [pathname, router, searchParams])

    const updateLineSelection = useCallback((selection: LogLineSelection) => {
        const normalized = normalizeLogLineSelection(selection.start, selection.end)
        if (!normalized) {
            return
        }

        setLineSelection(normalized)
        setIsSelectionUnavailable(false)

        const url = new URL(window.location.href)
        url.hash = formatLogLineSelection(normalized)
        window.history.replaceState(window.history.state, "", url)
    }, [])

    const clearLineSelection = useCallback(() => {
        if (!lineSelection && !window.location.hash) {
            return
        }

        setLineSelection(null)
        setSelectionAnchor(null)
        setIsSelectionUnavailable(false)

        const url = new URL(window.location.href)
        url.hash = ""
        window.history.replaceState(window.history.state, "", url)
    }, [lineSelection])

    const handleLineClick = useCallback((
        lineNumber: number,
        event: ReactMouseEvent<HTMLAnchorElement>
    ) => {
        event.preventDefault()

        if (event.shiftKey) {
            const anchor = selectionAnchor ?? lineSelection?.start ?? lineNumber
            const range = normalizeLogLineSelection(anchor, lineNumber)
            if (range) {
                updateLineSelection(range)
            }
            return
        }

        setSelectionAnchor(lineNumber)
        updateLineSelection({ start: lineNumber, end: lineNumber })
    }, [lineSelection?.start, selectionAnchor, updateLineSelection])

    const copyLogLines = useCallback(async (selection: LogLineSelection) => {
        try {
            const text = getSelectedLogText(logs.map((log) => log.message), selection)
            await navigator.clipboard.writeText(text)
            toast.success(selection.start === selection.end ? "Log line copied" : "Log lines copied")
        } catch {
            toast.error("Failed to copy log lines")
        }
    }, [logs])

    const copyPermalink = useCallback(async (selection: LogLineSelection) => {
        try {
            updateLineSelection(selection)

            const url = new URL(window.location.href)
            url.hash = formatLogLineSelection(selection)
            await navigator.clipboard.writeText(url.toString())
            toast.success("Log permalink copied")
        } catch {
            toast.error("Failed to copy log permalink")
        }
    }, [updateLineSelection])

    useEffect(() => {
        setDownloadFilename(`${jobId}-logs`)
    }, [jobId])

    useEffect(() => {
        const pendingSearchQuery = pendingSearchQueryRef.current
        if (pendingSearchQuery !== null) {
            if (searchQuery === pendingSearchQuery) {
                pendingSearchQueryRef.current = null
            }
            return
        }

        setSearchInput(searchQuery)
    }, [searchQuery])

    useEffect(() => {
        setStream(streamFilter || "all")
    }, [streamFilter])

    useEffect(() => {
        deepLinkFetchInFlightRef.current = false
        setIsSelectionUnavailable(false)
    }, [datasetKey])

    useEffect(() => {
        const syncSelectionFromFragment = () => {
            const selection = parseLogLineSelection(window.location.hash)
            setLineSelection(selection)
            setSelectionAnchor(selection?.start ?? null)
            setIsSelectionUnavailable(false)
        }

        syncSelectionFromFragment()
        window.addEventListener("hashchange", syncSelectionFromFragment)
        window.addEventListener("popstate", syncSelectionFromFragment)

        return () => {
            window.removeEventListener("hashchange", syncSelectionFromFragment)
            window.removeEventListener("popstate", syncSelectionFromFragment)
        }
    }, [workflowId, jobId])

    useEffect(() => {
        if (!lineSelection || isLogsLoading || isWorkflowLoading || logsError) {
            return
        }

        if (logs.length >= lineSelection.end) {
            setIsSelectionUnavailable(false)

            if (lastScrolledSelectionRef.current !== selectionKey) {
                lastScrolledSelectionRef.current = selectionKey
                requestAnimationFrame(() => {
                    virtuosoRef.current?.scrollToIndex({
                        index: lineSelection.start - 1,
                        align: "start",
                    })
                })
            }
            return
        }

        if (shouldFetchMoreLogsForSelection(lineSelection, logs.length, Boolean(hasNextPage))) {
            if (isFetchingNextPage || deepLinkFetchInFlightRef.current) {
                return
            }

            deepLinkFetchInFlightRef.current = true
            void fetchNextPage().finally(() => {
                deepLinkFetchInFlightRef.current = false
            })
            return
        }

        if (!isFetchingNextPage && hasNextPage === false) {
            setIsSelectionUnavailable(true)
        }
    }, [
        fetchNextPage,
        hasNextPage,
        isFetchingNextPage,
        isLogsLoading,
        isWorkflowLoading,
        lineSelection,
        logs.length,
        logsError,
        selectionKey,
    ])

    useEffect(() => {
        if (!isSelectionUnavailable || !lineSelection) {
            return
        }

        if (lastUnavailableSelectionRef.current === selectionKey) {
            return
        }

        lastUnavailableSelectionRef.current = selectionKey
        toast.warning(getUnavailableSelectionMessage(lineSelection))
    }, [isSelectionUnavailable, lineSelection, selectionKey])

    // Debounced search update
    useEffect(() => {
        const timer = setTimeout(() => {
            if (searchInput !== searchQuery) {
                pendingSearchQueryRef.current = searchInput
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

    // Parse log message and render token-scoped Meili highlights safely.
    const parseLog = useCallback(
        (message: string, highlightToken?: string) => {
            if (!parseJson) {
                return renderHighlightedText(message, highlightToken)
            }

            const { rawMessage, highlightedSegments } = parseHighlightedMessage(message, highlightToken)

            try {
                const parsed = JSON.parse(rawMessage)
                return renderHighlightedSegments(JSON.stringify(parsed, null, 2), highlightedSegments)
            } catch {
                const formattedMessage = rawMessage.replace(jsonRegex, (match) => {
                    try {
                        const parsed = JSON.parse(match)
                        return JSON.stringify(parsed, null, 2)
                    } catch {
                        return match
                    }
                })

                return renderHighlightedSegments(formattedMessage, highlightedSegments)
            }
        },
        [parseJson]
    )

    // Row renderer for Virtuoso
    const LogRow = (index: number) => {
        const log = logs[index]
        if (!log) return null
        const lineNumber = index + 1
        const isSelected = isLogLineSelected(lineNumber, lineSelection)
        const isTopSelectedLine = isSelected && lineSelection?.start === lineNumber
        const showLineOptions = !isSelected || isTopSelectedLine
        const actionSelection = isTopSelectedLine && lineSelection
            ? lineSelection
            : { start: lineNumber, end: lineNumber }
        const canCopyLines = actionSelection.end <= logs.length
        const canCopyPermalink = Boolean(
            shareableJobStatuses.has(jobStatus) &&
            !disableLogInteractions &&
            canCopyLines
        )
        const formattedMessage = parseLog(log.message, log.highlightToken)
        return (
            <div
                id={`L${lineNumber}`}
                className={cn(
                    "flex min-h-6 hover:bg-muted/50 group",
                    getLogStreamStyles(log.stream),
                    { "bg-accent hover:bg-accent": isSelected }
                )}
                data-line-number={lineNumber}
                data-selected={isSelected || undefined}
            >
                <div className="flex w-9 shrink-0 items-center justify-center">
                    {showLineOptions && (
                        <DropdownMenu>
                            <Tooltip>
                                <TooltipTrigger asChild>
                                    <DropdownMenuTrigger asChild>
                                        <Button
                                            variant="outline"
                                            size="icon"
                                            className={cn(
                                                "size-7 opacity-0 group-hover:opacity-100 data-[state=open]:opacity-100 focus-visible:opacity-100",
                                                { "opacity-100": isTopSelectedLine }
                                            )}
                                            aria-label={`Line ${lineNumber} options`}
                                        >
                                            <Ellipsis />
                                        </Button>
                                    </DropdownMenuTrigger>
                                </TooltipTrigger>
                                <TooltipContent side="left">
                                    Line {lineNumber} options
                                </TooltipContent>
                            </Tooltip>
                            <DropdownMenuContent align="start" className="min-w-40">
                                <DropdownMenuGroup>
                                    <DropdownMenuItem
                                        disabled={!canCopyLines}
                                        onSelect={() => void copyLogLines(actionSelection)}
                                    >
                                        <Copy />
                                        {actionSelection.start === actionSelection.end ? "Copy line" : "Copy lines"}
                                    </DropdownMenuItem>
                                    <DropdownMenuItem
                                        disabled={!canCopyPermalink}
                                        onSelect={() => void copyPermalink(actionSelection)}
                                    >
                                        <Link />
                                        Copy permalink
                                    </DropdownMenuItem>
                                </DropdownMenuGroup>
                            </DropdownMenuContent>
                        </DropdownMenu>
                    )}
                </div>
                <a
                    href={`#L${lineNumber}`}
                    onClick={(event) => handleLineClick(lineNumber, event)}
                    className={cn(
                        "flex w-11 shrink-0 items-start justify-end border-r px-2 py-1 text-xs tabular-nums text-muted-foreground select-none",
                        { "text-foreground": isSelected }
                    )}
                    aria-label={`Select log line ${lineNumber}`}
                    aria-current={isSelected ? "location" : undefined}
                >
                    {lineNumber}
                </a>
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
                            onChange={(e) => {
                                setSearchInput(e.target.value)
                                clearLineSelection()
                            }}
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
                                                clearLineSelection()
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
                        <Popover open={downloadPopoverOpen} onOpenChange={setDownloadPopoverOpen}>
                            <PopoverTrigger asChild>
                                <Button
                                    variant="outline"
                                    size="sm"
                                    disabled={disableLogInteractions || isDownloadLogsMutationLoading || logs.length === 0 || jobStatus === "RUNNING"}
                                >
                                    {isDownloadLogsMutationLoading ? (
                                        <Loader2 className="mr-2 h-4 w-4 animate-spin" />
                                    ) : (
                                        <Download className="h-4 w-4 mr-2" />
                                    )}
                                    Download
                                </Button>
                            </PopoverTrigger>
                            <PopoverContent className="w-64 m-2 flex flex-col gap-4" align="end">
                                <div className="flex flex-col gap-2">
                                    <Label htmlFor="download-filename" className="text-md">File name</Label>
                                    <Input
                                        id="download-filename"
                                        value={downloadFilename}
                                        onChange={(e) => setDownloadFilename(e.target.value)}
                                        disabled={isDownloadLogsMutationLoading}
                                    />
                                </div>
                                <div className="flex flex-col gap-2">
                                    <Label className="text-md">File type</Label>
                                    <Separator />
                                    <RadioGroup
                                        value={downloadFormat}
                                        onValueChange={(value) => setDownloadFormat(value as DownloadLogsFormat)}
                                        className="flex flex-col gap-2"
                                        disabled={isDownloadLogsMutationLoading}
                                    >
                                        <div className="flex items-center gap-2">
                                            <RadioGroupItem value="txt" id="download-format-txt" />
                                            <Label htmlFor="download-format-txt" className="text-sm font-medium">
                                                txt
                                            </Label>
                                        </div>
                                        <div className="flex items-center gap-2">
                                            <RadioGroupItem value="json" id="download-format-json" />
                                            <Label htmlFor="download-format-json" className="text-sm font-medium">
                                                json
                                            </Label>
                                        </div>
                                        <div className="flex items-center gap-2">
                                            <RadioGroupItem value="jsonl" id="download-format-jsonl" />
                                            <Label htmlFor="download-format-jsonl" className="text-sm font-medium">
                                                jsonl
                                            </Label>
                                        </div>
                                    </RadioGroup>
                                </div>
                                <Button
                                    onClick={() => {
                                        downloadLogsMutation.mutate(
                                            { filename: downloadFilename, format: downloadFormat },
                                            { onSuccess: () => setDownloadPopoverOpen(false) }
                                        )
                                    }}
                                    disabled={isDownloadLogsMutationLoading}
                                    className="w-full"
                                >
                                    {isDownloadLogsMutationLoading ? (
                                        <Loader2 className="mr-2 h-4 w-4 animate-spin" />
                                    ) : (
                                        <Download className="h-4 w-4 mr-2" />
                                    )}
                                    Download
                                </Button>
                            </PopoverContent>
                        </Popover>
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
                        ref={virtuosoRef}
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
