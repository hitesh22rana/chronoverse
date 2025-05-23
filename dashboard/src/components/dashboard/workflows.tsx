"use client"

import { useState, useEffect } from "react"
import { useSearchParams } from "next/navigation"
import {
    Search,
    Filter,
    RefreshCw,
    ChevronLeft,
    ChevronRight,
    X,
} from "lucide-react"

import { Input } from "@/components/ui/input"
import { Button } from "@/components/ui/button"
import { Badge } from "@/components/ui/badge"
import {
    Select,
    SelectContent,
    SelectItem,
    SelectTrigger,
    SelectValue,
} from "@/components/ui/select"
import {
    Popover,
    PopoverContent,
    PopoverTrigger,
} from "@/components/ui/popover"
import { Label } from "@/components/ui/label"
import { Separator } from "@/components/ui/separator"

import { WorkflowCard, WorkflowCardSkeleton } from "@/components/dashboard/workflows-card"
import { EmptyState } from "@/components/dashboard/empty-state"

import { useWorkflows } from "@/hooks/use-workflows"

import { cn } from "@/lib/utils"

export function Workflows() {
    const searchParams = useSearchParams()

    const urlSearchQuery = searchParams.get("query") || ""

    const [searchInput, setSearchInput] = useState(urlSearchQuery)
    const [isFiltersOpen, setIsFiltersOpen] = useState(false)

    // Local state for filter inputs (to be applied when "Apply Filters" is clicked)
    const [filterState, setFilterState] = useState({
        status: "",
        kind: "",
        intervalMin: "",
        intervalMax: "",
        createdAfter: "",
        createdBefore: ""
    })

    const {
        workflows,
        isLoading,
        searchQuery,
        statusFilter,
        kindFilter,
        intervalMin,
        intervalMax,
        createdAfter,
        createdBefore,
        updateSearchQuery,
        updateStatusFilter,
        updateKindFilter,
        updateIntervalFilter,
        updateDateFilter,
        clearAllFilters,
        pagination,
        refetch,
        refetchLoading
    } = useWorkflows()

    // Sync local search input with URL when URL changes
    useEffect(() => {
        setSearchInput(urlSearchQuery)
    }, [urlSearchQuery])

    // Sync filter state with current URL params
    useEffect(() => {
        setFilterState({
            status: statusFilter || "",
            kind: kindFilter || "",
            intervalMin: intervalMin || "",
            intervalMax: intervalMax || "",
            createdAfter: createdAfter || "",
            createdBefore: createdBefore || ""
        })
    }, [statusFilter, kindFilter, intervalMin, intervalMax, createdAfter, createdBefore])

    // Debounced search update
    useEffect(() => {
        const timer = setTimeout(() => {
            if (searchInput !== searchQuery) {
                updateSearchQuery(searchInput)
            }
        }, 300)

        return () => clearTimeout(timer)
    }, [searchInput, searchQuery, updateSearchQuery])

    const handleSearch = (e: React.ChangeEvent<HTMLInputElement>) => {
        setSearchInput(e.target.value)
    }

    const handleRefresh = () => {
        refetch()
    }

    const handleApplyFilters = () => {
        // Apply all filters at once
        if (filterState.status !== statusFilter) {
            updateStatusFilter(filterState.status || "ALL")
        }
        if (filterState.kind !== kindFilter) {
            updateKindFilter(filterState.kind || "ALL")
        }
        if (filterState.intervalMin !== intervalMin || filterState.intervalMax !== intervalMax) {
            updateIntervalFilter(filterState.intervalMin, filterState.intervalMax)
        }
        if (filterState.createdAfter !== createdAfter || filterState.createdBefore !== createdBefore) {
            updateDateFilter(filterState.createdAfter, filterState.createdBefore)
        }

        setIsFiltersOpen(false)
    }

    const handleClearFilters = () => {
        clearAllFilters()
        setFilterState({
            status: "",
            kind: "",
            intervalMin: "",
            intervalMax: "",
            createdAfter: "",
            createdBefore: ""
        })
        setIsFiltersOpen(false)
    }

    // Count active filters
    const activeFiltersCount = [
        statusFilter,
        kindFilter,
        intervalMin,
        intervalMax,
        createdAfter,
        createdBefore
    ].filter(Boolean).length

    return (
        <div className="flex flex-col h-full w-full mt-8">
            {/* Clean control bar */}
            <div className="space-y-4 mb-4">
                {/* Main controls row */}
                <div className="flex flex-row h-full w-full items-center justify-between gap-4">
                    {/* Search box */}
                    <div className="relative flex w-full">
                        <Search className="absolute left-2.5 top-2.5 size-4 text-muted-foreground" />
                        <Input
                            placeholder="Search workflows..."
                            value={searchInput}
                            onChange={handleSearch}
                            className="w-full pl-9 h-9"
                        />
                    </div>

                    <div className="flex items-center gap-2 justify-end">
                        {/* Filters popover */}
                        <Popover open={isFiltersOpen} onOpenChange={setIsFiltersOpen}>
                            <PopoverTrigger asChild>
                                <Button variant="outline" className="h-9">
                                    <Filter className="size-3" />
                                    <span className="sm:not-sr-only sr-only">
                                        Filters
                                    </span>
                                    {activeFiltersCount > 0 && (
                                        <Badge variant="secondary" className="ml-2 h-5 min-w-5 text-xs">
                                            {activeFiltersCount}
                                        </Badge>
                                    )}
                                </Button>
                            </PopoverTrigger>
                            <PopoverContent className="max-w-xs w-full m-2" align="center">
                                <div className="space-y-4">
                                    <div className="flex items-center justify-between">
                                        <h4 className="font-medium">Filter Workflows</h4>
                                        {activeFiltersCount > 0 && (
                                            <Button
                                                variant="ghost"
                                                size="sm"
                                                onClick={handleClearFilters}
                                                className="h-8 text-muted-foreground hover:text-foreground"
                                            >
                                                <X className="size-3 mr-1" />
                                                Clear all
                                            </Button>
                                        )}
                                    </div>

                                    <Separator />

                                    <div className="flex flex-row w-full gap-2">
                                        {/* Status Filter */}
                                        <div className="flex flex-col w-full gap-2">
                                            <Label>Status</Label>
                                            <Select
                                                value={filterState.status || "ALL"}
                                                onValueChange={(value) =>
                                                    setFilterState(prev => ({ ...prev, status: value === "ALL" ? "" : value }))
                                                }
                                            >
                                                <SelectTrigger className="w-full">
                                                    <SelectValue placeholder="All statuses" />
                                                </SelectTrigger>
                                                <SelectContent>
                                                    <SelectItem value="ALL">All statuses</SelectItem>
                                                    <SelectItem value="QUEUED">Queued</SelectItem>
                                                    <SelectItem value="STARTED">Building</SelectItem>
                                                    <SelectItem value="COMPLETED">Active</SelectItem>
                                                    <SelectItem value="FAILED">Failed</SelectItem>
                                                    <SelectItem value="CANCELED">Canceled</SelectItem>
                                                    <SelectItem value="TERMINATED">Terminated</SelectItem>
                                                </SelectContent>
                                            </Select>
                                        </div>

                                        {/* Kind Filter */}
                                        <div className="flex flex-col w-full gap-2">
                                            <Label>Kind</Label>
                                            <Select
                                                value={filterState.kind || "ALL"}
                                                onValueChange={(value) =>
                                                    setFilterState(prev => ({ ...prev, kind: value === "ALL" ? "" : value }))
                                                }
                                            >
                                                <SelectTrigger className="w-full">
                                                    <SelectValue placeholder="All kinds" />
                                                </SelectTrigger>
                                                <SelectContent>
                                                    <SelectItem value="ALL">All kinds</SelectItem>
                                                    <SelectItem value="HEARTBEAT">Heartbeat</SelectItem>
                                                    <SelectItem value="CONTAINER">Container</SelectItem>
                                                </SelectContent>
                                            </Select>
                                        </div>
                                    </div>

                                    <Separator />

                                    {/* Interval Range Filter */}
                                    <div className="space-y-2">
                                        <Label>Interval Range (minutes)</Label>
                                        <div className="grid grid-cols-2 gap-2">
                                            <div>
                                                <Input
                                                    type="number"
                                                    placeholder="Min"
                                                    value={filterState.intervalMin}
                                                    onChange={(e) =>
                                                        setFilterState(prev => ({ ...prev, intervalMin: e.target.value }))
                                                    }
                                                />
                                            </div>
                                            <div>
                                                <Input
                                                    type="number"
                                                    placeholder="Max"
                                                    value={filterState.intervalMax}
                                                    onChange={(e) =>
                                                        setFilterState(prev => ({ ...prev, intervalMax: e.target.value }))
                                                    }
                                                />
                                            </div>
                                        </div>
                                    </div>

                                    <Separator />

                                    {/* Apply button */}
                                    <Button onClick={handleApplyFilters} className="w-full">
                                        Apply Filters
                                    </Button>
                                </div>
                            </PopoverContent>
                        </Popover>

                        {/* Refresh button */}
                        <Button
                            variant="outline"
                            size="icon"
                            onClick={handleRefresh}
                            disabled={(isLoading || refetchLoading)}
                            className={cn(
                                "h-9 w-9",
                                (isLoading || refetchLoading) && "cursor-not-allowed"
                            )}
                        >
                            <RefreshCw className={cn(
                                "size-4",
                                (isLoading || refetchLoading) && "animate-spin"
                            )} />
                            <span className="sr-only">Refresh</span>
                        </Button>

                        {/* Pagination controls */}
                        <div className="flex items-center gap-1">
                            <Button
                                variant="outline"
                                size="icon"
                                onClick={() => pagination.goToPreviousPage()}
                                disabled={!pagination.hasPreviousPage}
                                className="h-9 w-9"
                            >
                                <ChevronLeft className="size-4" />
                                <span className="sr-only">Previous page</span>
                            </Button>
                            <Button
                                variant="outline"
                                size="icon"
                                onClick={() => pagination.goToNextPage()}
                                disabled={!pagination.hasNextPage}
                                className="h-9 w-9"
                            >
                                <ChevronRight className="size-4" />
                                <span className="sr-only">Next page</span>
                            </Button>
                        </div>
                    </div>
                </div>
            </div>

            <div className="flex-1 flex flex-col">
                {(isLoading) && workflows.length === 0 ? (
                    <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
                        {[...Array(6)].map((_, i) => (
                            <WorkflowCardSkeleton key={i} />
                        ))}
                    </div>
                ) : workflows.length === 0 ? (
                    <EmptyState
                        title="No workflows found"
                        description={activeFiltersCount > 0 || searchQuery
                            ? 'Try adjusting your search or filters'
                            : 'Create your first workflow to get started'}
                    />
                ) : (
                    <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
                        {workflows.map((workflow) => (
                            <WorkflowCard
                                key={workflow.id}
                                workflow={workflow}
                            />
                        ))}
                    </div>
                )}
            </div>
        </div>
    )
}