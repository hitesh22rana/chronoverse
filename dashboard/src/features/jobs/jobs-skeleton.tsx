import { Card, CardContent, CardHeader } from "@/components/ui/card"
import { Skeleton } from "@/components/ui/skeleton"

export function WorkflowJobsSkeleton() {
    return (
        <div className="grid grid-cols-1 xl:grid-cols-2 gap-4">
            {Array(10).fill(0).map((_, i) => (
                <JobCardSkeleton key={i} />
            ))}
        </div>
    )
}

function JobCardSkeleton() {
    return (
        <Card className="overflow-hidden relative">
            <div className="absolute top-0.5 right-0.5 rotate-12 border-b">
                <Skeleton className="h-4 w-4" />
            </div>
            <CardHeader className="flex md:items-center items-start justify-between md:pb-3.5 pb-2.5">
                <div className="flex md:flex-row flex-col justify-start md:items-center items-start gap-2">
                    <Skeleton className="h-6 w-24" />
                    <Skeleton className="h-5 md:w-80 w-44" />
                </div>
                <Skeleton className="h-4 w-28" />
            </CardHeader>
            <CardContent className="md:pt-4 pt-0 space-y-3">
                <div className="grid grid-cols-1 md:grid-cols-3 md:gap-4 gap-6">
                    {[...Array(3)].map((_, i) => (
                        <div key={i} className="md:space-y-1 space-y-2">
                            <Skeleton className="h-3 w-16" />
                            <div className="flex items-center gap-1.5">
                                <Skeleton className="h-3.5 w-3.5" />
                                <Skeleton className="h-4 w-28" />
                            </div>
                        </div>
                    ))}
                </div>
            </CardContent>
        </Card>
    )
}
