"use client"

import { useState } from "react"

import { Badge } from "@/components/ui/badge"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { cn } from "@/lib/utils"
import { getStatusLabel, getStatusMeta } from "@/features/jobs/job-status"
import { getTerminalReason } from "@/features/jobs/terminal-reason"

type JobStatusBadgeProps = {
    status?: string
    reasonMessage?: string
    revealReason?: boolean
    className?: string
}

export function JobStatusBadge({
    status,
    reasonMessage,
    revealReason = false,
    className,
}: JobStatusBadgeProps) {
    const [isOpen, setIsOpen] = useState(false)
    const meta = getStatusMeta(status)
    const Icon = meta.icon
    const reason = getTerminalReason(status, reasonMessage)
    const badge = (
        <Badge
            variant="outline"
            className={cn("flex items-center gap-1 px-2 py-1", meta.badgeClass, className)}
            aria-label={reason ? `${getStatusLabel(status, "job")}: ${reason}` : getStatusLabel(status, "job")}
        >
            <Icon className={cn("h-3 w-3", meta.iconClass)} />
            <span className="text-xs">{getStatusLabel(status, "job")}</span>
        </Badge>
    )

    if (!reason) return badge

    return (
        <Tooltip open={revealReason || isOpen} onOpenChange={setIsOpen}>
            <TooltipTrigger asChild>{badge}</TooltipTrigger>
            <TooltipContent side="bottom" sideOffset={6}>
                {reason}
            </TooltipContent>
        </Tooltip>
    )
}
