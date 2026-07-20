import type {
    MouseEvent as ReactMouseEvent,
    MutableRefObject,
    PointerEvent as ReactPointerEvent,
    ReactNode,
} from "react"
import { Copy, Ellipsis, Link } from "lucide-react"

import { Button } from "@/components/ui/button"
import {
    DropdownMenu,
    DropdownMenuContent,
    DropdownMenuGroup,
    DropdownMenuItem,
    DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { isLogLineSelected, type LogLineSelection } from "@/features/logs/log-line-selection"
import type { JobLog } from "@/features/logs/types"
import { cn } from "@/lib/utils"

type PointerGesture = {
    startX: number
    startY: number
    didDrag: boolean
}

export type LogRowModel = {
    logs: JobLog[]
    lineSelection: LogLineSelection | null
    jobStatus: string
    disableLogInteractions: boolean
    parseLog: (_message: string, _highlightToken?: string) => ReactNode
    handleLogRowPointerDown: (_event: ReactPointerEvent<HTMLButtonElement>) => void
    handleLogRowPointerMove: (_event: ReactPointerEvent<HTMLButtonElement>) => void
    rowPointerGestureRef: MutableRefObject<PointerGesture | null>
    handleLogRowClick: (_lineNumber: number, _event: ReactMouseEvent<HTMLButtonElement>) => void
    copyLogLines: (_selection: LogLineSelection) => Promise<void>
    copyPermalink: (_selection: LogLineSelection) => Promise<void>
}

const shareableJobStatuses = new Set(["COMPLETED", "FAILED", "CANCELED"])

const getLogStreamStyles = (stream: string) =>
    stream === "stderr" ? "text-muted-foreground bg-red-50 dark:bg-red-800/20" : ""

const getLogStreamStripStyles = (stream: string) =>
    stream === "stderr"
        ? "bg-red-500 dark:bg-red-400"
        : "bg-emerald-500 dark:bg-emerald-400"

export function LogRow({ index, model }: { index: number; model: LogRowModel }) {
    const {
        logs,
        lineSelection,
        jobStatus,
        disableLogInteractions,
        parseLog,
        handleLogRowPointerDown,
        handleLogRowPointerMove,
        rowPointerGestureRef,
        handleLogRowClick,
        copyLogLines,
        copyPermalink,
    } = model

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
        shareableJobStatuses.has(jobStatus) && !disableLogInteractions && canCopyLines,
    )

    return (
        <div
            id={`L${lineNumber}`}
            className={cn(
                "group flex min-h-6 hover:bg-muted/50",
                getLogStreamStyles(log.stream),
                { "bg-accent hover:bg-accent": isSelected },
            )}
            data-line-number={lineNumber}
            data-selected={isSelected || undefined}
        >
            <div className="flex w-9 shrink-0 items-center justify-center" data-line-options>
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
                                            { "opacity-100": isTopSelectedLine },
                                        )}
                                        aria-label="Line options"
                                    >
                                        <Ellipsis />
                                    </Button>
                                </DropdownMenuTrigger>
                            </TooltipTrigger>
                            <TooltipContent side="left">Line options</TooltipContent>
                        </Tooltip>
                        <DropdownMenuContent align="start" className="min-w-40" data-line-options>
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
            <span
                className={cn("my-1 w-1 flex-none rounded-sm", getLogStreamStripStyles(log.stream))}
                title={log.stream}
                aria-hidden="true"
            />
            <button
                type="button"
                className="flex-1 whitespace-pre-wrap break-all px-3 py-1 text-left"
                aria-label="Select log entry"
                aria-pressed={isSelected}
                onPointerDown={handleLogRowPointerDown}
                onPointerMove={handleLogRowPointerMove}
                onPointerCancel={() => {
                    rowPointerGestureRef.current = null
                }}
                onClick={(event) => handleLogRowClick(lineNumber, event)}
            >
                {parseLog(log.message, log.highlightToken)}
            </button>
        </div>
    )
}
