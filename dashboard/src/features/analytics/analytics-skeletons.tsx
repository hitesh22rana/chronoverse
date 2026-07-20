import {
    Card,
    CardContent,
    CardHeader,
} from "@/components/ui/card"
import { Skeleton } from "@/components/ui/skeleton"

import { cn } from "@/lib/utils"

const userMetricSkeletons = ["workflows", "jobs", "logs", "runtime"] as const

const workflowMetricSkeletons = [
    { id: "jobs", valueWidth: "w-20" },
    { id: "runtime", valueWidth: "w-24" },
    { id: "average", valueWidth: "w-16" },
    { id: "logs", valueWidth: "w-20" },
] as const

export function UserAnalyticsOverviewSkeleton() {
    return (
        <div className="flex flex-col gap-4">
            <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-4">
                {userMetricSkeletons.map((metric) => (
                    <Skeleton key={metric} className="h-32 w-full" />
                ))}
            </div>
            <div className="grid grid-cols-1 gap-4 xl:grid-cols-2">
                <Skeleton className="h-96 w-full" />
                <Skeleton className="h-96 w-full" />
            </div>
        </div>
    )
}

export function WorkflowAnalyticsCardsSkeleton() {
    return (
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-4">
            {workflowMetricSkeletons.map(({ id, valueWidth }) => (
                <Card key={id} className="gap-4 py-4">
                    <CardHeader className="grid grid-cols-[1fr_auto] px-4">
                        <Skeleton className="h-5 w-28" />
                        <Skeleton className="size-8 rounded-md" />
                    </CardHeader>
                    <CardContent className="flex flex-col gap-1 px-4">
                        <Skeleton className={cn("h-7", valueWidth)} />
                        <Skeleton className="h-3 w-full max-w-44" />
                    </CardContent>
                </Card>
            ))}
        </div>
    )
}
