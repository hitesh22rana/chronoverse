import type { LucideIcon } from "lucide-react"

import { Badge } from "@/components/ui/badge"
import {
    Card,
    CardContent,
    CardDescription,
    CardHeader,
} from "@/components/ui/card"

type AnalyticsMetricCardProps = {
    helper: string
    icon: LucideIcon
    label: string
    value: string
}

export function AnalyticsMetricCard({
    helper,
    icon: Icon,
    label,
    value,
}: AnalyticsMetricCardProps) {
    return (
        <Card className="gap-4 py-4">
            <CardHeader className="grid grid-cols-[1fr_auto] px-4">
                <CardDescription>{label}</CardDescription>
                <Badge variant="secondary" className="size-8 p-0">
                    <Icon />
                </Badge>
            </CardHeader>
            <CardContent className="flex min-w-0 flex-col gap-1 px-4">
                <p className="text-2xl font-semibold tracking-tight tabular-nums">{value}</p>
                <p className="truncate text-xs text-muted-foreground">{helper}</p>
            </CardContent>
        </Card>
    )
}
