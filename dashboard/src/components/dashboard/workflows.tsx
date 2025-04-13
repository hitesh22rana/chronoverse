"use client"

import { useState, useEffect } from "react"
import { useRouter, useSearchParams } from "next/navigation"
import Link from "next/link"
import { formatDistanceToNow } from "date-fns"
import { Search, Filter, RefreshCw, Clock, Code, ArrowUpRight } from "lucide-react"

import { Input } from "@/components/ui/input"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { Card, CardContent, CardFooter } from "@/components/ui/card"
import {
    Select,
    SelectContent,
    SelectItem,
    SelectTrigger,
    SelectValue,
} from "@/components/ui/select"
import {
    DropdownMenu,
    DropdownMenuContent,
    DropdownMenuGroup,
    DropdownMenuItem,
    DropdownMenuTrigger
} from "@/components/ui/dropdown-menu"

import { useWorkflows } from "@/hooks/use-workflows"

import { cn } from "@/lib/utils"

export function WorkflowStatus({ status, isTerminated }: { status: string, isTerminated: boolean | null }) {
    if (isTerminated) {
        return (
            <div className="inline-flex items-center rounded-full border px-2.5 py-0.5 text-xs font-semibold transition-colors focus:outline-none focus:ring-2 focus:ring-ring focus:ring-offset-2 bg-gray-100 dark:bg-gray-800 text-gray-500 border-gray-300 dark:border-gray-600">
                Terminated
            </div>
        )
    }

    switch (status) {
        case "COMPLETED":
            return (
                <div className="inline-flex items-center rounded-full border px-2.5 py-0.5 text-xs font-semibold transition-colors focus:outline-none focus:ring-2 focus:ring-ring focus:ring-offset-2 bg-green-100 dark:bg-green-900/20 text-green-600 dark:text-green-400 border-green-200 dark:border-green-800">
                    Active
                </div>
            )
        case "QUEUED":
        case "STARTED":
            return (
                <div className="inline-flex items-center rounded-full border px-2.5 py-0.5 text-xs font-semibold transition-colors focus:outline-none focus:ring-2 focus:ring-ring focus:ring-offset-2 bg-blue-100 dark:bg-blue-900/20 text-blue-600 dark:text-blue-400 border-blue-200 dark:border-blue-800">
                    Building
                </div>
            )
        case "FAILED":
            return (
                <div className="inline-flex items-center rounded-full border px-2.5 py-0.5 text-xs font-semibold transition-colors focus:outline-none focus:ring-2 focus:ring-ring focus:ring-offset-2 bg-red-100 dark:bg-red-900/20 text-red-600 dark:text-red-400 border-red-200 dark:border-red-800">
                    Failed
                </div>
            )
        default:
            return (
                <div className="inline-flex items-center rounded-full border px-2.5 py-0.5 text-xs font-semibold transition-colors focus:outline-none focus:ring-2 focus:ring-ring focus:ring-offset-2">
                    {status}
                </div>
            )
    }
}

export function Workflows() {
    const router = useRouter()
    const searchParams = useSearchParams()

    // Get search and filter values from URL
    const urlSearchQuery = searchParams.get("search") || ""
    const urlStatusFilter = searchParams.get("status") || "ALL"

    const [searchDebounce, setSearchDebounce] = useState(urlSearchQuery)
    const [searchQuery, setSearchQuery] = useState(urlSearchQuery)

    const {
        workflows,
        isLoading,
        terminateWorkflow,
        pagination,
        refetch
    } = useWorkflows()

    // Sync URL status filter with local state
    const statusFilter = urlStatusFilter === "ALL" ? null : urlStatusFilter

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

    // Filter workflows based on search and status
    const filteredWorkflows = workflows.filter(workflow => {
        const matchesSearch = !searchQuery ||
            workflow.name.toLowerCase().includes(searchQuery.toLowerCase())

        const matchesStatus = !statusFilter ||
            (statusFilter === "TERMINATED" ? !!workflow.terminatedAt : workflow.workflowBuildStatus === statusFilter)

        return matchesSearch && matchesStatus
    })

    return (
        <div className="space-y-4">
            <div className="flex flex-col sm:flex-row gap-4">
                <div className="relative flex-1">
                    <Search className="absolute left-2.5 top-2.5 h-4 w-4 text-muted-foreground" />
                    <Input
                        placeholder="Search workflows..."
                        value={searchQuery}
                        onChange={handleSearch}
                        className="w-full pl-9"
                    />
                </div>
                <div className="flex gap-2">
                    <Select
                        value={urlStatusFilter}
                        onValueChange={handleStatusFilter}
                    >
                        <SelectTrigger className="w-full sm:w-[180px]">
                            <div className="flex items-center gap-2">
                                <Filter className="h-4 w-4" />
                                <SelectValue placeholder="Filter by status" />
                            </div>
                        </SelectTrigger>
                        <SelectContent>
                            <SelectItem value="ALL">All statuses</SelectItem>
                            <SelectItem value="COMPLETED">Active</SelectItem>
                            <SelectItem value="QUEUED">Building</SelectItem>
                            <SelectItem value="FAILED">Failed</SelectItem>
                            <SelectItem value="TERMINATED">Terminated</SelectItem>
                        </SelectContent>
                    </Select>
                    <Button
                        variant="outline"
                        size="icon"
                        onClick={handleRefresh}
                        disabled={isLoading}
                    >
                        <RefreshCw className={cn(
                            "h-4 w-4",
                            isLoading && "animate-spin"
                        )} />
                        <span className="sr-only">Refresh</span>
                    </Button>
                </div>
            </div>

            <div className="mt-4">
                {isLoading && workflows.length === 0 ? (
                    // Skeleton loading state for cards
                    <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
                        {[...Array(6)].map((_, i) => (
                            <Card key={i} className="overflow-hidden">
                                <CardContent className="p-0">
                                    <div className="p-6">
                                        <div className="flex justify-between items-start mb-4">
                                            <Skeleton className="h-6 w-40" />
                                            <Skeleton className="h-5 w-16 rounded-full" />
                                        </div>
                                        <div className="space-y-2">
                                            <Skeleton className="h-4 w-20" />
                                            <Skeleton className="h-4 w-24" />
                                        </div>
                                    </div>
                                </CardContent>
                                <CardFooter className="p-4 border-t bg-muted/20 flex justify-between">
                                    <Skeleton className="h-4 w-24" />
                                    <Skeleton className="h-8 w-8 rounded-full" />
                                </CardFooter>
                            </Card>
                        ))}
                    </div>
                ) : filteredWorkflows.length === 0 ? (
                    <div className="text-center py-12">
                        <div className="mx-auto flex h-12 w-12 items-center justify-center rounded-full bg-muted">
                            <svg
                                xmlns="http://www.w3.org/2000/svg"
                                width="24"
                                height="24"
                                viewBox="0 0 24 24"
                                fill="none"
                                stroke="currentColor"
                                strokeWidth="2"
                                strokeLinecap="round"
                                strokeLinejoin="round"
                                className="text-muted-foreground"
                            >
                                <circle cx="12" cy="12" r="10" />
                                <line x1="12" x2="12" y1="8" y2="16" />
                                <line x1="8" x2="16" y1="12" y2="12" />
                            </svg>
                        </div>
                        <h3 className="mt-4 text-lg font-medium">No workflows found</h3>
                        <p className="mt-2 text-muted-foreground">
                            {searchQuery || statusFilter ? 'Try adjusting your search or filters' : 'Create your first workflow to get started'}
                        </p>
                    </div>
                ) : (
                    <div>
                        <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
                            {filteredWorkflows.map((workflow) => {
                                let statusClass = "border-muted";
                                if (workflow.terminatedAt) {
                                    statusClass = "border-gray-300 dark:border-gray-700";
                                } else if (workflow.workflowBuildStatus === "COMPLETED") {
                                    statusClass = "border-green-200 dark:border-green-800";
                                } else if (["QUEUED", "STARTED"].includes(workflow.workflowBuildStatus)) {
                                    statusClass = "border-blue-200 dark:border-blue-800";
                                } else if (workflow.workflowBuildStatus === "FAILED") {
                                    statusClass = "border-red-200 dark:border-red-800";
                                }

                                return (
                                    <Card
                                        key={workflow.id}
                                        className={cn(
                                            "overflow-hidden hover:shadow-md transition-all",
                                            `border-l-4 ${statusClass}`
                                        )}
                                    >
                                        <CardContent className="p-0">
                                            <div className="p-6">
                                                <div className="flex justify-between items-start mb-4">
                                                    <h3 className="font-medium text-lg truncate" title={workflow.name}>
                                                        {workflow.name}
                                                    </h3>
                                                    <WorkflowStatus
                                                        status={workflow.workflowBuildStatus}
                                                        isTerminated={workflow.terminatedAt ? true : null}
                                                    />
                                                </div>
                                                <div className="space-y-2 text-sm text-muted-foreground">
                                                    <div className="flex items-center gap-2">
                                                        {workflow.kind === "HTTP" ? (
                                                            <svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" className="text-primary/70">
                                                                <circle cx="12" cy="12" r="10" />
                                                                <line x1="2" y1="12" x2="22" y2="12" />
                                                                <path d="M12 2a15.3 15.3 0 0 1 4 10 15.3 15.3 0 0 1-4 10 15.3 15.3 0 0 1-4-10 15.3 15.3 0 0 1 4-10z" />
                                                            </svg>
                                                        ) : (
                                                            <Code className="h-4 w-4 text-primary/70" />
                                                        )}
                                                        <span>{workflow.kind} Workflow</span>
                                                    </div>
                                                    <div className="flex items-center gap-2">
                                                        <Clock className="h-4 w-4 text-primary/70" />
                                                        <span>Runs every {workflow.interval} minutes</span>
                                                    </div>
                                                </div>
                                            </div>
                                        </CardContent>
                                        <CardFooter className="p-4 border-t bg-muted/20 flex justify-between">
                                            <div className="text-xs text-muted-foreground">
                                                Created {formatDistanceToNow(new Date(workflow.createdAt), { addSuffix: true })}
                                            </div>
                                            <div className="flex gap-2">
                                                <Button
                                                    variant="ghost"
                                                    size="sm"
                                                    className="h-8 px-2"
                                                    asChild
                                                >
                                                    <Link href={`/workflows/${workflow.id}`} className="flex items-center gap-1">
                                                        Details
                                                        <ArrowUpRight className="h-3 w-3 ml-1" />
                                                    </Link>
                                                </Button>
                                                {!workflow.terminatedAt && (
                                                    <DropdownMenu>
                                                        <DropdownMenuTrigger asChild>
                                                            <Button variant="ghost" size="icon" className="h-8 w-8">
                                                                <svg xmlns="http://www.w3.org/2000/svg" width="15" height="15" viewBox="0 0 15 15" fill="none" className="h-4 w-4">
                                                                    <path d="M3.625 7.5C3.625 8.12132 3.12132 8.625 2.5 8.625C1.87868 8.625 1.375 8.12132 1.375 7.5C1.375 6.87868 1.87868 6.375 2.5 6.375C3.12132 6.375 3.625 6.87868 3.625 7.5ZM8.625 7.5C8.625 8.12132 8.12132 8.625 7.5 8.625C6.87868 8.625 6.375 8.12132 6.375 7.5C6.375 6.87868 6.87868 6.375 7.5 6.375C8.12132 6.375 8.625 6.87868 8.625 7.5ZM13.625 7.5C13.625 8.12132 13.1213 8.625 12.5 8.625C11.8787 8.625 11.375 8.12132 11.375 7.5C11.375 6.87868 11.8787 6.375 12.5 6.375C13.1213 6.375 13.625 6.87868 13.625 7.5Z" fill="currentColor" fillRule="evenodd" clipRule="evenodd"></path>
                                                                </svg>
                                                            </Button>
                                                        </DropdownMenuTrigger>
                                                        <DropdownMenuContent align="end">
                                                            <DropdownMenuGroup>
                                                                <DropdownMenuItem
                                                                    onClick={() => terminateWorkflow(workflow.id)}
                                                                    className="text-red-500 focus:text-red-500"
                                                                >
                                                                    Terminate
                                                                </DropdownMenuItem>
                                                            </DropdownMenuGroup>
                                                        </DropdownMenuContent>
                                                    </DropdownMenu>
                                                )}
                                            </div>
                                        </CardFooter>
                                    </Card>
                                );
                            })}
                        </div>

                        {filteredWorkflows.length > 0 && (
                            <div className="flex items-center justify-between mt-6">
                                <div className="text-sm text-muted-foreground">
                                    {filteredWorkflows.length} workflow{filteredWorkflows.length !== 1 ? 's' : ''}
                                </div>
                                <div className="flex items-center space-x-2">
                                    <Button
                                        variant="outline"
                                        size="icon"
                                        onClick={() => pagination.goToPreviousPage()}
                                        disabled={!pagination.hasPreviousPage}
                                    >
                                        <svg xmlns="http://www.w3.org/2000/svg" width="15" height="15" viewBox="0 0 15 15" fill="none" className="h-4 w-4">
                                            <path d="M8.84182 3.13514C9.04327 3.32401 9.05348 3.64042 8.86462 3.84188L5.43521 7.49991L8.86462 11.1579C9.05348 11.3594 9.04327 11.6758 8.84182 11.8647C8.64036 12.0535 8.32394 12.0433 8.13508 11.8419L4.38508 7.84188C4.20477 7.64955 4.20477 7.35027 4.38508 7.15794L8.13508 3.15794C8.32394 2.95648 8.64036 2.94628 8.84182 3.13514Z" fill="currentColor" fillRule="evenodd" clipRule="evenodd"></path>
                                        </svg>
                                        <span className="sr-only">Previous page</span>
                                    </Button>
                                    <Button
                                        variant="outline"
                                        size="icon"
                                        onClick={() => pagination.goToNextPage()}
                                        disabled={!pagination.hasNextPage}
                                    >
                                        <svg xmlns="http://www.w3.org/2000/svg" width="15" height="15" viewBox="0 0 15 15" fill="none" className="h-4 w-4">
                                            <path d="M6.1584 3.13508C6.35985 2.94621 6.67627 2.95642 6.86514 3.15788L10.6151 7.15788C10.7954 7.3502 10.7954 7.64949 10.6151 7.84182L6.86514 11.8418C6.67627 12.0433 6.35985 12.0535 6.1584 11.8646C5.95694 11.6757 5.94673 11.3593 6.1356 11.1579L9.565 7.49985L6.1356 3.84182C5.94673 3.64036 5.95694 3.32394 6.1584 3.13508Z" fill="currentColor" fillRule="evenodd" clipRule="evenodd"></path>
                                        </svg>
                                        <span className="sr-only">Next page</span>
                                    </Button>
                                </div>
                            </div>
                        )}
                    </div>
                )}
            </div>
        </div>
    )
}