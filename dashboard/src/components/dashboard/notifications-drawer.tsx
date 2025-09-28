import {
    useEffect,
    useMemo,
    useState,
} from "react";
import Link from "next/link";
import {
    formatDistanceToNow,
    isToday,
    isYesterday,
    format,
} from "date-fns";
import {
    Bell,
    Check,
} from "lucide-react";

import {
    Virtuoso,
    Components
} from "react-virtuoso"

import {
    Sheet,
    SheetContent,
    SheetHeader,
    SheetTitle,
} from "@/components/ui/sheet"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Skeleton } from "@/components/ui/skeleton"

import {
    useNotifications,
    type Notification,
    type NotificationPayload,
} from "@/hooks/use-notifications"

import { cn } from "@/lib/utils"

type Severity = "ERROR" | "ALERT" | "INFO" | "SUCCESS"

const KIND_TO_SEVERITY: Record<string, Severity> = {
    WEB_ERROR: "ERROR",
    WEB_ALERT: "ALERT",
    WEB_WARN: "ALERT",
    WEB_INFO: "INFO",
    WEB_SUCCESS: "SUCCESS",
}

const SEVERITY_META: Record<Severity, { color: string }> = {
    ALERT: {
        color:
            "border-l-4 border-red-500 bg-red-500/5 dark:bg-red-900/20 dark:text-red-100 text-red-900",
    },
    ERROR: {
        color:
            "border-l-4 border-orange-500 bg-orange-500/5 dark:bg-orange-900/20 dark:text-orange-100 text-orange-900",
    },
    INFO: {
        color:
            "border-l-4 border-blue-500 bg-blue-500/5 dark:bg-blue-900/20 dark:text-blue-100 text-blue-900",
    },
    SUCCESS: {
        color:
            "border-l-4 border-green-500 bg-green-500/5 dark:bg-green-900/20 dark:text-green-100 text-green-900",
    },
}

function dateHeading(iso: string) {
    const d = new Date(iso)
    if (isToday(d)) return "Today"
    if (isYesterday(d)) return "Yesterday"
    return format(d, "MMM d, yyyy")
}

function highlightNotification(message: string): string {
    return message.replace(
        /'([^']+)'/g,
        (_, p1) => `<span class="font-semibold">${p1}</span>`
    );
}

interface NotificationsDrawerProps {
    open: boolean
    onClose: () => void
}

type FlatRow =
    | { type: "heading"; heading: string }
    | { type: "item"; n: Notification }

export function NotificationsDrawer({ open, onClose }: NotificationsDrawerProps) {
    const {
        notifications,
        isLoading,
        markAsRead,
        fetchNextPage,
        isFetchingNextPage,
        hasNextPage,
    } = useNotifications()

    // Group notifications by date and flatten with headings
    const flat = useMemo<FlatRow[]>(() => {
        if (notifications.length === 0) return []
        const groups = new Map<string, Notification[]>()

        for (const n of notifications) {
            const key = dateHeading(n.created_at)
            const arr = groups.get(key) ?? []
            arr.push(n)
            groups.set(key, arr)
        }

        const result: FlatRow[] = []
        for (const [heading, arr] of groups.entries()) {
            result.push({ type: "heading", heading })
            for (const n of arr) result.push({ type: "item", n })
        }
        return result
    }, [notifications])

    // Bulk selection
    const [selected, setSelected] = useState<Set<string>>(new Set())
    const [selectAll, setSelectAll] = useState(false)

    const toggleSelect = (id: string) => {
        setSelected((prev) => {
            const next = new Set(prev)
            // eslint-disable-next-line @typescript-eslint/no-unused-expressions
            next.has(id) ? next.delete(id) : next.add(id)
            return next
        })
    }

    const handleSelectAll = () => {
        if (selectAll) {
            setSelected(new Set())
            setSelectAll(false)
        } else {
            const allIds = new Set(notifications.map((n) => n.id))
            setSelected(allIds)
            setSelectAll(true)
        }
    }

    const clearSelection = () => {
        setSelected(new Set())
        setSelectAll(false)
    }

    useEffect(() => {
        if (notifications.length === 0) {
            setSelectAll(false)
            return
        }
        const allSelected =
            notifications.length > 0 && notifications.every((n) => selected.has(n.id))
        setSelectAll(allSelected)
    }, [selected, notifications])

    const bulkRead = () => {
        if (selected.size === 0) return
        markAsRead(Array.from(selected))
        clearSelection()
    }

    // Item renderer
    const renderRow = (index: number) => {
        const d = flat[index]
        if (!d) return null

        if (d.type === "heading") {
            return (
                <div className="sticky bg-background text-sm font-semibold border-b px-4 py-2">
                    {d.heading}
                </div>
            )
        }

        const n = d.n
        const payload: NotificationPayload = JSON.parse(n.payload)
        const sev = KIND_TO_SEVERITY[n.kind] ?? "INFO"
        const meta = SEVERITY_META[sev]

        return (
            <div
                className={cn(
                    "relative px-2 py-4 flex gap-3 group hover:bg-muted/30 transition-colors cursor-pointer",
                    meta.color,
                    !n.read_at && "ring-1 ring-white/5"
                )}
                onClick={() => {
                    if (!n.read_at) markAsRead([n.id])
                }}
            >
                <div
                    onClick={(e) => {
                        e.stopPropagation()
                        toggleSelect(n.id)
                    }}
                >
                    <Checkbox checked={selected.has(n.id)} />
                </div>

                <Link href={payload.action_url} prefetch={false} className="flex-1 min-w-0 flex flex-col gap-2">
                    <div className="flex items-center justify-between gap-2">
                        <p className="font-medium text-sm truncate">{payload.title}</p>
                        <span className="text-xs text-muted-foreground shrink-0">
                            {formatDistanceToNow(new Date(n.created_at), { addSuffix: true })}
                        </span>
                    </div>

                    <p
                        className="text-xs font-medium text-muted-foreground line-clamp-2"
                        dangerouslySetInnerHTML={{ __html: highlightNotification(payload.message) }}
                    />
                </Link>
            </div>
        )
    }

    const handleEndReached = () => {
        if (hasNextPage && !isFetchingNextPage) {
            fetchNextPage()
        }
    }

    const virtuosoComponents: Components = {
        Footer: () =>
            isFetchingNextPage ? (
                <div className="flex items-center justify-center py-3">
                    <Skeleton className="h-4 w-24" />
                </div>
            ) : null,
    }

    return (
        <Sheet open={open} onOpenChange={onClose}>
            <SheetContent className="w-full sm:max-w-md p-0 gap-0 h-full flex flex-col">
                {/* header */}
                <SheetHeader className="px-6 py-4 border-b flex-shrink-0">
                    <div className="flex items-center gap-2">
                        <Bell className="h-5 w-5" />
                        <SheetTitle>Notifications</SheetTitle>
                    </div>
                </SheetHeader>

                {/* bulk bar */}
                <div className="px-4 py-2 border-b flex items-center justify-between gap-3 bg-background/95">
                    <div className="flex items-center gap-3">
                        <Checkbox
                            checked={selectAll}
                            onCheckedChange={handleSelectAll}
                            aria-label="Select all notifications"
                        />
                        <p className="text-sm font-medium">{selected.size || 0} selected</p>
                    </div>
                    <Button
                        size="sm"
                        variant="ghost"
                        className="gap-1 cursor-pointer"
                        onClick={bulkRead}
                        disabled={!selected.size}
                    >
                        <Check className="h-4 w-4" /> Mark as read
                    </Button>
                </div>

                {/* list / loading / empty */}
                <div className="flex-1 relative">
                    {isLoading && notifications.length === 0 ? (
                        [...Array(10)].map((_, i) => (
                            <div key={i} className="px-6 py-4 space-y-3">
                                <Skeleton className="h-4 w-3/4" />
                                <Skeleton className="h-3 w-full" />
                                <Skeleton className="h-3 w-1/2" />
                            </div>
                        ))
                    ) : notifications.length === 0 ? (
                        <div className="flex flex-col items-center justify-center p-8 text-center gap-2 text-muted-foreground">
                            <Bell className="h-8 w-8" />
                            <p className="font-medium">You&apos;re all caught up!</p>
                            <p className="text-sm">We&apos;ll notify you when there&apos;s new activity.</p>
                        </div>
                    ) : (
                        <div className="h-full">
                            <Virtuoso
                                style={{ height: "100%", width: "100%" }}
                                totalCount={flat.length}
                                itemContent={renderRow}
                                endReached={handleEndReached}
                                overscan={250}
                                components={virtuosoComponents}
                            />
                        </div>
                    )}
                </div>
            </SheetContent>
        </Sheet>
    )
}
