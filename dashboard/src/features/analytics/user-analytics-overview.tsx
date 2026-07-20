"use client"

import {
    Activity,
    ChartNoAxesColumnIncreasing,
    Clock3,
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

import {
    Card,
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
    Empty,
    EmptyDescription,
    EmptyHeader,
    EmptyMedia,
    EmptyTitle,
} from "@/components/ui/empty"

import { AnalyticsMetricCard } from "@/features/analytics/analytics-metric-card"
import {
    divide,
    formatInteger,
    formatJobsPerWorkflow,
    formatLogsPerJob,
    formatWorkflowKind,
    truncateWorkflowLabel,
    withTerminalJobActivity,
} from "@/features/analytics/analytics-utils"

import type { UserAnalytics } from "@/features/analytics/types"
import { formatSeconds } from "@/lib/utils"

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
] as const

const topWorkflowsLimit = 10

type UserAnalyticsOverviewProps = {
    analytics: UserAnalytics
}

export function UserAnalyticsOverview({ analytics }: UserAnalyticsOverviewProps) {
    const workloadData = withTerminalJobActivity(analytics.workflow_kinds).map((item, index) => ({
        ...item,
        display_kind: formatWorkflowKind(item.kind),
        fill: chartColors[index % chartColors.length],
    }))
    const topWorkflows = withTerminalJobActivity(analytics.top_workflows)
        .slice(0, topWorkflowsLimit)
    const jobsPerWorkflow = formatJobsPerWorkflow(analytics.total_jobs, analytics.total_workflows)
    const secondsPerJob = divide(analytics.total_job_execution_duration, analytics.total_jobs)
    const logsPerJob = formatLogsPerJob(analytics.total_joblogs, analytics.total_jobs)

    return (
        <div className="flex flex-col gap-4">
            <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-4">
                <AnalyticsMetricCard
                    label="Workflows"
                    value={formatInteger(analytics.total_workflows)}
                    helper={jobsPerWorkflow ?? "No terminal jobs recorded"}
                    icon={Workflow}
                />
                <AnalyticsMetricCard
                    label="Terminal jobs"
                    value={formatInteger(analytics.total_jobs)}
                    helper={`${formatSeconds(Math.round(secondsPerJob))} average runtime`}
                    icon={Activity}
                />
                <AnalyticsMetricCard
                    label="Generated logs"
                    value={formatInteger(analytics.total_joblogs)}
                    helper={logsPerJob ?? "No logs generated"}
                    icon={ScrollText}
                />
                <AnalyticsMetricCard
                    label="Execution time"
                    value={formatSeconds(analytics.total_job_execution_duration)}
                    helper={`Across ${formatInteger(analytics.total_jobs)} terminal jobs`}
                    icon={Clock3}
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
                                                                {formatInteger(analytics.total_jobs)}
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
                                                    {formatInteger(item.total_jobs)}
                                                </span>
                                            </div>
                                        </div>
                                    ))}
                                </div>
                            </div>
                        ) : (
                            <Empty className="min-h-64 border-0 p-4 md:p-6">
                                <EmptyHeader>
                                    <EmptyMedia variant="icon">
                                        <Activity />
                                    </EmptyMedia>
                                    <EmptyTitle className="text-base">
                                        No terminal job activity yet
                                    </EmptyTitle>
                                    <EmptyDescription>
                                        Run a workflow to see terminal jobs grouped by workflow kind.
                                    </EmptyDescription>
                                </EmptyHeader>
                            </Empty>
                        )}
                    </CardContent>
                </Card>

                <Card>
                    <CardHeader>
                        <CardTitle>Most active workflows</CardTitle>
                        <CardDescription>
                            Top {topWorkflowsLimit} ranked by durable terminal-job count
                        </CardDescription>
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
                                        tickFormatter={truncateWorkflowLabel}
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
                            <Empty className="min-h-64 border-0 p-4 md:p-6">
                                <EmptyHeader>
                                    <EmptyMedia variant="icon">
                                        <ChartNoAxesColumnIncreasing />
                                    </EmptyMedia>
                                    <EmptyTitle className="text-base">
                                        No workflow activity yet
                                    </EmptyTitle>
                                    <EmptyDescription>
                                        Complete a workflow run to see your most active workflows ranked here.
                                    </EmptyDescription>
                                </EmptyHeader>
                            </Empty>
                        )}
                    </CardContent>
                </Card>
            </div>
        </div>
    )
}
