"use client"

import { useState } from "react"
import { BarChart3, RefreshCw } from "lucide-react"

import { UserAnalyticsOverview } from "@/features/analytics/user-analytics-overview"
import { UserAnalyticsOverviewSkeleton } from "@/features/analytics/analytics-skeletons"
import { useUserAnalytics } from "@/features/analytics/use-user-analytics"

import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
    Card,
    CardAction,
    CardDescription,
    CardHeader,
    CardTitle,
} from "@/components/ui/card"
import {
    Drawer,
    DrawerContent,
    DrawerDescription,
    DrawerHeader,
    DrawerTitle,
    DrawerTrigger,
} from "@/components/ui/drawer"

import { cn } from "@/lib/utils"

export function UserAnalyticsDrawer() {
    const [open, setOpen] = useState(false)
    const analyticsQuery = useUserAnalytics(open)

    return (
        <Drawer direction="bottom" open={open} onOpenChange={setOpen} handleOnly>
            <DrawerTrigger asChild>
                <Button variant="outline" className="w-full cursor-pointer md:w-fit">
                    <BarChart3 data-icon="inline-start" />
                    Analytics
                </Button>
            </DrawerTrigger>
            <DrawerContent className="overflow-hidden" style={{ userSelect: "text" }}>
                <DrawerHeader className="flex w-full shrink-0 flex-row items-center gap-3 px-4 py-4 text-left md:px-6">
                    <Badge variant="secondary" className="size-9 p-0">
                        <BarChart3 />
                    </Badge>
                    <div className="flex min-w-0 flex-1 flex-col gap-1">
                        <DrawerTitle>Analytics overview</DrawerTitle>
                        <DrawerDescription>
                            Durable activity across all workflows, including deleted history
                        </DrawerDescription>
                    </div>
                    <Button
                        variant="ghost"
                        size="icon"
                        onClick={() => analyticsQuery.refetch()}
                        disabled={analyticsQuery.isFetching}
                        aria-label="Refresh analytics"
                    >
                        <RefreshCw className={cn(analyticsQuery.isFetching && "animate-spin")} />
                    </Button>
                </DrawerHeader>

                <div className="min-h-0 overscroll-contain overflow-y-auto px-4 pb-6 [scrollbar-width:none] md:px-6 [&::-webkit-scrollbar]:hidden">
                    {analyticsQuery.isPending ? (
                        <UserAnalyticsOverviewSkeleton />
                    ) : analyticsQuery.isError ? (
                        <Card>
                            <CardHeader>
                                <CardTitle>Analytics unavailable</CardTitle>
                                <CardDescription>{analyticsQuery.error.message}</CardDescription>
                                <CardAction>
                                    <Button variant="outline" size="sm" onClick={() => analyticsQuery.refetch()}>
                                        Try again
                                    </Button>
                                </CardAction>
                            </CardHeader>
                        </Card>
                    ) : analyticsQuery.data ? (
                        <UserAnalyticsOverview analytics={analyticsQuery.data} />
                    ) : null}
                </div>
            </DrawerContent>
        </Drawer>
    )
}
