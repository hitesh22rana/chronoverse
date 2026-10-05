"use client"

import { ChevronLeft, ChevronRight, Filter, Loader2, Play, RefreshCw, X } from "lucide-react"

import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Label } from "@/components/ui/label"
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Separator } from "@/components/ui/separator"

import type { Workflow } from "@/features/workflows/types"
import { cn } from "@/lib/utils"

export type WorkflowJobFilters = {
    status: string
    trigger: string
}

type WorkflowJobsToolbarProps = {
    workflow?: Workflow
    filters: WorkflowJobFilters
    onFiltersChange: (_patch: Partial<WorkflowJobFilters>) => void
    isFiltersOpen: boolean
    onFiltersOpenChange: (_open: boolean) => void
    activeFiltersCount: number
    onApplyFilters: () => void
    onClearFilters: () => void
    onRefresh: () => void
    isRefreshPending: boolean
    isManualRunPending: boolean
    onManualRun: () => void
    hasNextPage: boolean
    hasPreviousPage: boolean
    onNextPage: () => void
    onPreviousPage: () => void
}

/**
 * Jobs tab toolbar. Filter selections stay local until Apply, so opening the
 * popover reseeds them from the applied URL filters instead of from whatever
 * was last picked.
 */
export function WorkflowJobsToolbar({
    workflow,
    filters,
    onFiltersChange,
    isFiltersOpen,
    onFiltersOpenChange,
    activeFiltersCount,
    onApplyFilters,
    onClearFilters,
    onRefresh,
    isRefreshPending,
    isManualRunPending,
    onManualRun,
    hasNextPage,
    hasPreviousPage,
    onNextPage,
    onPreviousPage,
}: WorkflowJobsToolbarProps) {
    return (
        <div className="flex flex-wrap items-center justify-end gap-2 w-full mb-4">
            {/* A manual run is only meaningful once the build succeeded and the
                workflow has not already been terminated. */}
            {!!workflow?.build_status &&
                workflow.build_status === "COMPLETED" &&
                !workflow?.terminated_at && (
                    <Button
                        variant="default"
                        size="sm"
                        className="cursor-pointer shrink-0 sm:max-w-[140px] w-full h-9"
                        onClick={() => onManualRun()}
                        disabled={isManualRunPending}
                    >
                        {isManualRunPending ? (
                            <Loader2 className="mr-2 h-4 w-4 animate-spin" />
                        ) : (
                            <Play className="h-4 w-4" />
                        )}
                        Manual run
                    </Button>
                )}

            <Popover open={isFiltersOpen} onOpenChange={onFiltersOpenChange}>
                <PopoverTrigger asChild>
                    <Button variant="outline" className="relative h-9">
                        <Filter className="size-3" />
                        <span className="sm:not-sr-only sr-only">Filters</span>
                        {activeFiltersCount > 0 && (
                            <Badge
                                variant="secondary"
                                className="absolute -right-1 -top-1.5 size-4 rounded-full p-0 flex items-center justify-center text-xs overflow-visible"
                            >
                                {activeFiltersCount}
                            </Badge>
                        )}
                    </Button>
                </PopoverTrigger>
                <PopoverContent className="min-w-xs w-full m-2" align="center">
                    <div className="space-y-4">
                        <div className="flex items-center justify-between">
                            <h4 className="font-medium">Filter by</h4>
                            {activeFiltersCount > 0 && (
                                <Button
                                    variant="ghost"
                                    size="sm"
                                    onClick={onClearFilters}
                                    className="h-8 text-muted-foreground hover:text-foreground"
                                >
                                    <X className="size-3 mr-1" />
                                    Clear all
                                </Button>
                            )}
                        </div>

                        <Separator />

                        <div className="flex flex-row gap-2 w-full">
                            <div className="flex flex-col gap-2 w-full">
                                <Label>Trigger</Label>
                                <Select
                                    value={filters.trigger || "ALL"}
                                    onValueChange={(value) =>
                                        onFiltersChange({ trigger: value === "ALL" ? "" : value })
                                    }
                                >
                                    <SelectTrigger className="w-full">
                                        <SelectValue placeholder="All triggers" />
                                    </SelectTrigger>
                                    <SelectContent>
                                        <SelectItem value="ALL">All triggers</SelectItem>
                                        <SelectItem value="AUTOMATIC">Automatic</SelectItem>
                                        <SelectItem value="MANUAL">Manual</SelectItem>
                                    </SelectContent>
                                </Select>
                            </div>

                            <div className="flex flex-col gap-2 w-full">
                                <Label>Status</Label>
                                <Select
                                    value={filters.status || "ALL"}
                                    onValueChange={(value) =>
                                        onFiltersChange({ status: value === "ALL" ? "" : value })
                                    }
                                >
                                    <SelectTrigger className="w-full">
                                        <SelectValue placeholder="All statuses" />
                                    </SelectTrigger>
                                    <SelectContent>
                                        <SelectItem value="ALL">All statuses</SelectItem>
                                        <SelectItem value="PENDING">Pending</SelectItem>
                                        <SelectItem value="QUEUED">Queued</SelectItem>
                                        <SelectItem value="RUNNING">Running</SelectItem>
                                        <SelectItem value="COMPLETED">Completed</SelectItem>
                                        <SelectItem value="FAILED">Failed</SelectItem>
                                        <SelectItem value="CANCELED">Canceled</SelectItem>
                                    </SelectContent>
                                </Select>
                            </div>
                        </div>

                        <Separator />

                        <Button onClick={onApplyFilters} className="w-full">
                            Apply Filters
                        </Button>
                    </div>
                </PopoverContent>
            </Popover>

            <Button
                variant="outline"
                size="icon"
                onClick={onRefresh}
                disabled={isRefreshPending}
                className={cn("h-9 w-9", isRefreshPending && "cursor-not-allowed")}
            >
                <RefreshCw className={cn("size-4", isRefreshPending && "animate-spin")} />
                <span className="sr-only">Refresh</span>
            </Button>

            <div className="flex items-center border-l pl-4 ml-1">
                <Button
                    variant="outline"
                    size="icon"
                    onClick={() => onPreviousPage()}
                    disabled={!hasPreviousPage}
                    className="h-9 w-9"
                >
                    <ChevronLeft className="size-4" />
                    <span className="sr-only">Previous page</span>
                </Button>
                <Button
                    variant="outline"
                    size="icon"
                    onClick={() => onNextPage()}
                    disabled={!hasNextPage}
                    className="h-9 w-9 ml-2"
                >
                    <ChevronRight className="size-4" />
                    <span className="sr-only">Next page</span>
                </Button>
            </div>
        </div>
    )
}