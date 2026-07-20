import type { LucideIcon } from "lucide-react"

import { Badge } from "@/components/ui/badge"
import {
    Card,
    CardContent,
    CardDescription,
    CardHeader,
} from "@/components/ui/card"

import { cn } from "@/lib/utils"

type AnalyticsMetricCardProps = {
    helper: string
    icon: LucideIcon
    label: string
    value: string
    variant?: "summary" | "workflow"
}

export function AnalyticsMetricCard({
    helper,
    icon: Icon,
    label,
    value,
    variant = "summary",
}: AnalyticsMetricCardProps) {
    const isWorkflowMetric = variant === "workflow"

    return (
        <Card className={cn("gap-4 py-4", isWorkflowMetric && "h-full min-h-39")}>
            <CardHeader className="grid grid-cols-[1fr_auto] px-4">
                <CardDescription>{label}</CardDescription>
                <Badge variant="secondary" className="size-8 p-0">
                    <Icon />
                </Badge>
            </CardHeader>
            <CardContent className="flex flex-col gap-1 px-4">
                <p className="text-2xl font-semibold tracking-tight tabular-nums">{value}</p>
                <p className={cn("text-xs text-muted-foreground", isWorkflowMetric && "min-h-8")}>
                    {helper}
                </p>
            </CardContent>
        </Card>
    )
}
