"use client"

import {
    Activity,
    BarChart3,
    Clock,
    ScrollText,
    Workflow,
} from "lucide-react"

import {
    Drawer,
    DrawerContent,
    DrawerDescription,
    DrawerHeader,
    DrawerTitle,
    DrawerTrigger,
} from "@/components/ui/drawer"
import {
    Card,
    CardContent,
    CardHeader,
    CardTitle
} from "@/components/ui/card"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"

import { useUsers } from "@/hooks/use-users"

import { formatSeconds } from "@/lib/utils"

export function UserAnalyticsDrawer() {
    const { userAnalytics, isAnalyticsLoading } = useUsers()

    return (
        <Drawer direction="bottom">
            <DrawerTrigger asChild>
                <Button variant="outline" className="w-full md:w-fit cursor-pointer">
                    <BarChart3 className="mr-2 h-4 w-4" />
                    Analytics
                </Button>
            </DrawerTrigger>
            <DrawerContent>
                <DrawerHeader className="flex flex-row items-center justify-start py-3 gap-2 w-full">
                    <BarChart3 className="size-5 text-muted-foreground" />
                    <div className="flex flex-col items-start justify-start">
                        <DrawerTitle className="text-base text-left">Analytics overview</DrawerTitle>
                        <DrawerDescription className="text-left text-xs">Aggregated metrics across all your workflows</DrawerDescription>
                    </div>
                </DrawerHeader>
                <div className="px-4 pb-4 h-full w-full">
                    <div className="max-h-[calc(80vh-100px)] overflow-y-auto pr-1">
                        {isAnalyticsLoading ? (
                            <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
                                <Skeleton className="h-24 w-full" />
                                <Skeleton className="h-24 w-full" />
                                <Skeleton className="h-24 w-full" />
                                <Skeleton className="h-24 w-full" />
                            </div>
                        ) : (
                            <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
                                <StatCard
                                    label="Total Workflows"
                                    value={(userAnalytics?.total_workflows ?? 0).toLocaleString()}
                                    icon={<Workflow className="h-3.5 w-3.5" />}
                                    tone="blue"
                                />
                                <StatCard
                                    label="Jobs Executed"
                                    value={(userAnalytics?.total_jobs ?? 0).toLocaleString()}
                                    icon={<Activity className="h-3.5 w-3.5" />}
                                    tone="emerald"
                                />
                                <StatCard
                                    label="Log Entries"
                                    value={(userAnalytics?.total_joblogs ?? 0).toLocaleString()}
                                    icon={<ScrollText className="h-3.5 w-3.5" />}
                                    tone="amber"
                                />
                                <StatCard
                                    label="Total Exec. Time"
                                    value={formatSeconds(userAnalytics?.total_job_execution_duration ?? 0)}
                                    icon={<Clock className="h-3.5 w-3.5" />}
                                    tone="indigo"
                                />
                            </div>
                        )}
                    </div>
                </div>
            </DrawerContent>
        </Drawer>
    )
}

type StatCardProps = {
    label: string
    value: string
    icon: React.ReactNode
    tone?: "blue" | "emerald" | "amber" | "indigo"
}

function StatCard({ label, value, icon, tone = "blue" }: StatCardProps) {
    const bgMap = {
        blue: "from-blue-50 to-indigo-50 dark:from-blue-950/20 dark:to-indigo-950/20",
        emerald: "from-emerald-50 to-green-50 dark:from-emerald-950/20 dark:to-green-950/20",
        amber: "from-amber-50 to-orange-50 dark:from-amber-950/20 dark:to-orange-950/20",
        indigo: "from-indigo-50 to-violet-50 dark:from-indigo-950/20 dark:to-violet-950/20",
    } as const

    const iconBgMap = {
        blue: "bg-blue-100 dark:bg-blue-900/30 text-blue-600 dark:text-blue-400",
        emerald: "bg-emerald-100 dark:bg-emerald-900/30 text-emerald-600 dark:text-emerald-400",
        amber: "bg-amber-100 dark:bg-amber-900/30 text-amber-600 dark:text-amber-400",
        indigo: "bg-indigo-100 dark:bg-indigo-900/30 text-indigo-600 dark:text-indigo-400",
    } as const

    const valueColorMap = {
        blue: "text-blue-700 dark:text-blue-300",
        emerald: "text-emerald-700 dark:text-emerald-300",
        amber: "text-amber-700 dark:text-amber-300",
        indigo: "text-indigo-700 dark:text-indigo-300",
    } as const

    return (
        <Card className="relative overflow-hidden border">
            <div className={`absolute inset-0 bg-gradient-to-br ${bgMap[tone]}`} />
            <CardHeader className="relative pb-1 flex flex-row items-center justify-between">
                <CardTitle className="text-[11px] tracking-wide text-muted-foreground font-medium">
                    {label}
                </CardTitle>
                <div className={`h-7 w-7 rounded-md flex items-center justify-center ${iconBgMap[tone]}`}>
                    {icon}
                </div>
            </CardHeader>
            <CardContent className="relative">
                <div className={`text-2xl font-semibold leading-tight ${valueColorMap[tone]}`}>{value}</div>
            </CardContent>
        </Card>
    )
}

export default UserAnalyticsDrawer
