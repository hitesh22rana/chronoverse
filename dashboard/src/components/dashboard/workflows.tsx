"use client"

import { useState, useEffect } from "react"
import { useRouter, useSearchParams } from "next/navigation"
import { Search, Filter, RefreshCw, ChevronLeft, ChevronRight } from "lucide-react"

import { Input } from "@/components/ui/input"
import { Button } from "@/components/ui/button"
import {
    Select,
    SelectContent,
    SelectItem,
    SelectTrigger,
    SelectValue,
} from "@/components/ui/select"

import { WorkflowCard, WorkflowCardSkeleton } from "@/components/dashboard/workflows-card"
import { EmptyState } from "@/components/dashboard/empty-state"
import { useWorkflows } from "@/hooks/use-workflows"
import { cn } from "@/lib/utils"

export function Workflows() {
    const router = useRouter()
    const searchParams = useSearchParams()

    const urlSearchQuery = searchParams.get("search") || ""
    const urlStatusFilter = searchParams.get("status") || "ALL"

    const [searchDebounce, setSearchDebounce] = useState(urlSearchQuery)
    const [searchQuery, setSearchQuery] = useState(urlSearchQuery)

    const {
        workflows,
        isLoading,
        pagination,
        refetch,
        refetchLoading
    } = useWorkflows()

    // Update URL with filters
    useEffect(() => {
        const timer = setTimeout(() => {
            const params = new URLSearchParams(searchParams.toString())

            if (searchDebounce) {
                params.set("search", searchDebounce)
            } else {
                params.delete("search")
            }

            // Don't update if nothing changed
            if (searchDebounce !== urlSearchQuery) {
                router.push(`?${params.toString()}`, { scroll: false })
            }
        }, 300) // 300ms debounce

        return () => clearTimeout(timer)
    }, [searchDebounce, router, searchParams, urlSearchQuery])

    const handleSearch = (e: React.ChangeEvent<HTMLInputElement>) => {
        setSearchQuery(e.target.value)
        setSearchDebounce(e.target.value)
    }

    const handleStatusFilter = (value: string) => {
        const params = new URLSearchParams(searchParams.toString())

        if (value === "ALL") {
            params.delete("status")
        } else {
            params.set("status", value)
        }

        router.push(`?${params.toString()}`, { scroll: false })
    }

    const handleRefresh = () => {
        refetch()
    }

    return (
        <div className="flex flex-col h-full mt-8">
            {/* Improved control bar layout */}
            <div className="flex md:flex-row flex-col items-center justify-between gap-5 mb-4">
                {/* Search box */}
                <div className="relative flex-1 w-full">
                    <Search className="absolute left-2.5 top-2.5 size-4 text-muted-foreground" />
                    <Input
                        placeholder="Search workflows..."
                        value={searchQuery}
                        onChange={handleSearch}
                        className="w-full pl-9 h-9"
                    />
                </div>

                <div className="flex items-center gap-2 sm:w-fit w-full">
                    {/* Status filter */}
                    <Select
                        value={urlStatusFilter}
                        onValueChange={handleStatusFilter}
                    >
                        <SelectTrigger className="min-w-[150px] w-full h-9">
                            <div className="flex items-center gap-2 text-sm">
                                <Filter className="size-3" />
                                <SelectValue placeholder="All statuses" />
                            </div>
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
                    <div className="flex items-center border-l pl-4 ml-1">
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
                            className="h-9 w-9 ml-2"
                        >
                            <ChevronRight className="size-4" />
                            <span className="sr-only">Next page</span>
                        </Button>
                    </div>
                </div>
            </div>

            <div className="flex-1 flex flex-col">
                {(isLoading) && workflows.length === 0 ? (
                    <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
                        {[...Array(6)].map((_, i) => (
                            <WorkflowCardSkeleton
                                key={i}
                            />
                        ))}
                    </div>
                ) : workflows.length === 0 ? (
                    <EmptyState
                        title="No workflows found"
                        description={searchQuery || urlStatusFilter !== "ALL"
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