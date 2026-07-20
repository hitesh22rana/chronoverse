"use client"

import { useState } from "react"
import {
    Activity,
    BarChart3,
    Clock3,
    RefreshCw,
    ScrollText,
    Workflow,
} from "lucide-react"
import {
    Bar,
    BarChart,
    CartesianGrid,
    Cell,
    Label,
    Pie,
    PieChart,
    XAxis,
    YAxis,
} from "@/components/ui/recharts"

import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
    Card,
    CardAction,
    CardContent,
    CardDescription,
    CardHeader,
    CardTitle,
} from "@/components/ui/card"
import {
    ChartContainer,
    ChartTooltip,
    ChartTooltipContent,
    type ChartConfig,
} from "@/components/ui/chart"
import {
    Drawer,
    DrawerContent,
    DrawerDescription,
    DrawerHeader,
    DrawerTitle,
    DrawerTrigger,
} from "@/components/ui/drawer"
import { Skeleton } from "@/components/ui/skeleton"

import { useUserAnalytics } from "@/hooks/use-user-analytics"

import { cn, formatSeconds } from "@/lib/utils"

const workloadChartConfig = {
    jobs: {
        label: "Terminal jobs",
        color: "var(--chart-1)",
    },
} satisfies ChartConfig

const rankingChartConfig = {
    total_jobs: {
        label: "Terminal jobs",
        color: "var(--chart-2)",
    },
} satisfies ChartConfig

const chartColors = [
    "var(--chart-1)",
    "var(--chart-2)",
    "var(--chart-3)",
    "var(--chart-4)",
    "var(--chart-5)",
]

const decimalFormatter = new Intl.NumberFormat(undefined, { maximumFractionDigits: 1 })
const analyticsSkeletonCards = ["workflows", "jobs", "logs", "runtime"] as const

export function UserAnalyticsDrawer() {
    const [open, setOpen] = useState(false)
    const analyticsQuery = useUserAnalytics(open)
    const analytics = analyticsQuery.data

    const workloadData = (analytics?.workflow_kinds ?? []).map((item, index) => ({
        ...item,
        display_kind: formatKind(item.kind),
        fill: chartColors[index % chartColors.length],
    }))

    const topWorkflows = analytics?.top_workflows ?? []
    const jobsPerWorkflow = divide(analytics?.total_jobs, analytics?.total_workflows)
    const logsPerJob = divide(analytics?.total_joblogs, analytics?.total_jobs)
    const secondsPerJob = divide(analytics?.total_job_execution_duration, analytics?.total_jobs)

    return (
        <Drawer direction="bottom" open={open} onOpenChange={setOpen}>
            <DrawerTrigger asChild>
                <Button variant="outline" className="w-full cursor-pointer md:w-fit">
                    <BarChart3 data-icon="inline-start" />
                    Analytics
                </Button>
            </DrawerTrigger>
            <DrawerContent className="overflow-hidden">
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

                <div className="min-h-0 overflow-y-auto px-4 pb-6 md:px-6">
                    {analyticsQuery.isPending ? (
                        <AnalyticsSkeleton />
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
                    ) : (
                        <div className="flex flex-col gap-4">
                            <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-4">
                                <StatCard
                                    label="Workflows"
                                    value={(analytics?.total_workflows ?? 0).toLocaleString()}
                                    helper={`${formatDecimal(jobsPerWorkflow)} jobs per workflow`}
                                    icon={<Workflow />}
                                />
                                <StatCard
                                    label="Terminal jobs"
                                    value={(analytics?.total_jobs ?? 0).toLocaleString()}
                                    helper={`${formatSeconds(Math.round(secondsPerJob))} average runtime`}
                                    icon={<Activity />}
                                />
                                <StatCard
                                    label="Retained logs"
                                    value={(analytics?.total_joblogs ?? 0).toLocaleString()}
                                    helper={`${formatDecimal(logsPerJob)} logs per job`}
                                    icon={<ScrollText />}
                                />
                                <StatCard
                                    label="Execution time"
                                    value={formatSeconds(analytics?.total_job_execution_duration ?? 0)}
                                    helper={`Across ${(analytics?.total_jobs ?? 0).toLocaleString()} terminal jobs`}
                                    icon={<Clock3 />}
                                />
                            </div>

                            <div className="grid grid-cols-1 gap-4 xl:grid-cols-[minmax(0,0.8fr)_minmax(0,1.2fr)]">
                                <Card>
                                    <CardHeader>
                                        <CardTitle>Workload mix</CardTitle>
                                        <CardDescription>Terminal jobs grouped by workflow kind</CardDescription>
                                    </CardHeader>
                                    <CardContent>
                                        {workloadData.length > 0 ? (
                                            <div className="grid items-center gap-4 sm:grid-cols-[minmax(0,1fr)_minmax(10rem,0.7fr)] xl:grid-cols-1 2xl:grid-cols-[minmax(0,1fr)_minmax(10rem,0.7fr)]">
                                                <ChartContainer
                                                    config={workloadChartConfig}
                                                    className="mx-auto aspect-square max-h-64 w-full"
                                                >
                                                    <PieChart accessibilityLayer>
                                                        <ChartTooltip
                                                            cursor={false}
                                                            content={<ChartTooltipContent hideLabel />}
                                                        />
                                                        <Pie
                                                            data={workloadData}
                                                            dataKey="total_jobs"
                                                            nameKey="display_kind"
                                                            innerRadius={62}
                                                            outerRadius={88}
                                                            strokeWidth={4}
                                                        >
                                                            {workloadData.map((item) => (
                                                                <Cell key={item.kind} fill={item.fill} />
                                                            ))}
                                                            <Label
                                                                content={({ viewBox }) => {
                                                                    if (!viewBox || !("cx" in viewBox) || !("cy" in viewBox)) {
                                                                        return null
                                                                    }

                                                                    return (
                                                                        <text
                                                                            x={viewBox.cx}
                                                                            y={viewBox.cy}
                                                                            textAnchor="middle"
                                                                            dominantBaseline="middle"
                                                                        >
                                                                            <tspan
                                                                                x={viewBox.cx}
                                                                                y={viewBox.cy}
                                                                                className="fill-foreground text-2xl font-semibold"
                                                                            >
                                                                                {(analytics?.total_jobs ?? 0).toLocaleString()}
                                                                            </tspan>
                                                                            <tspan
                                                                                x={viewBox.cx}
                                                                                y={(viewBox.cy ?? 0) + 22}
                                                                                className="fill-muted-foreground"
                                                                            >
                                                                                terminal jobs
                                                                            </tspan>
                                                                        </text>
                                                                    )
                                                                }}
                                                            />
                                                        </Pie>
                                                    </PieChart>
                                                </ChartContainer>
                                                <div className="flex flex-col gap-3">
                                                    {workloadData.map((item) => (
                                                        <div key={item.kind} className="flex items-center gap-3">
                                                            <span
                                                                className="size-2.5 shrink-0 rounded-sm"
                                                                style={{ backgroundColor: item.fill }}
                                                            />
                                                            <div className="flex min-w-0 flex-1 items-baseline justify-between gap-3">
                                                                <span className="truncate text-sm text-muted-foreground">
                                                                    {item.display_kind}
                                                                </span>
                                                                <span className="font-mono text-sm font-medium tabular-nums">
                                                                    {item.total_jobs.toLocaleString()}
                                                                </span>
                                                            </div>
                                                        </div>
                                                    ))}
                                                </div>
                                            </div>
                                        ) : (
                                            <p className="py-16 text-center text-sm text-muted-foreground">
                                                No workload breakdown is available yet.
                                            </p>
                                        )}
                                    </CardContent>
                                </Card>

                                <Card>
                                    <CardHeader>
                                        <CardTitle>Most active workflows</CardTitle>
                                        <CardDescription>Ranked by durable terminal-job count</CardDescription>
                                    </CardHeader>
                                    <CardContent>
                                        {topWorkflows.length > 0 ? (
                                            <ChartContainer
                                                config={rankingChartConfig}
                                                className="h-72 w-full"
                                                initialDimension={{ width: 560, height: 288 }}
                                            >
                                                <BarChart
                                                    accessibilityLayer
                                                    data={topWorkflows}
                                                    layout="vertical"
                                                    margin={{ left: 4, right: 24 }}
                                                >
                                                    <CartesianGrid horizontal={false} />
                                                    <YAxis
                                                        dataKey="workflow_name"
                                                        type="category"
                                                        tickLine={false}
                                                        axisLine={false}
                                                        width={132}
                                                        tickFormatter={truncateLabel}
                                                    />
                                                    <XAxis dataKey="total_jobs" type="number" hide />
                                                    <ChartTooltip
                                                        cursor={false}
                                                        content={
                                                            <ChartTooltipContent
                                                                indicator="line"
                                                                labelKey="workflow_name"
                                                            />
                                                        }
                                                    />
                                                    <Bar
                                                        dataKey="total_jobs"
                                                        fill="var(--color-total_jobs)"
                                                        radius={5}
                                                    />
                                                </BarChart>
                                            </ChartContainer>
                                        ) : (
                                            <p className="py-16 text-center text-sm text-muted-foreground">
                                                No workflow ranking is available yet.
                                            </p>
                                        )}
                                    </CardContent>
                                </Card>
                            </div>
                        </div>
                    )}
                </div>
            </DrawerContent>
        </Drawer>
    )
}

type StatCardProps = {
    label: string
    value: string
    helper: string
    icon: React.ReactNode
}

function StatCard({ label, value, helper, icon }: StatCardProps) {
    return (
        <Card className="gap-4 py-4">
            <CardHeader className="grid grid-cols-[1fr_auto] px-4">
                <CardDescription>{label}</CardDescription>
                <Badge variant="secondary" className="size-8 p-0">
                    {icon}
                </Badge>
            </CardHeader>
            <CardContent className="flex flex-col gap-1 px-4">
                <p className="text-2xl font-semibold tracking-tight tabular-nums">{value}</p>
                <p className="text-xs text-muted-foreground">{helper}</p>
            </CardContent>
        </Card>
    )
}

function AnalyticsSkeleton() {
    return (
        <div className="flex flex-col gap-4">
            <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-4">
                {analyticsSkeletonCards.map((card) => (
                    <Skeleton key={card} className="h-32 w-full" />
                ))}
            </div>
            <div className="grid grid-cols-1 gap-4 xl:grid-cols-2">
                <Skeleton className="h-96 w-full" />
                <Skeleton className="h-96 w-full" />
            </div>
        </div>
    )
}

function divide(numerator = 0, denominator = 0) {
    return denominator > 0 ? numerator / denominator : 0
}

function formatDecimal(value: number) {
    return decimalFormatter.format(value)
}

function formatKind(kind: string) {
    return kind
        .toLocaleLowerCase()
        .split("_")
        .map((word) => word.charAt(0).toLocaleUpperCase() + word.slice(1))
        .join(" ")
}

function truncateLabel(value: string) {
    return value.length > 18 ? `${value.slice(0, 17)}…` : value
}

export default UserAnalyticsDrawer
