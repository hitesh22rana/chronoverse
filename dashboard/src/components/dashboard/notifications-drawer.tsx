"use client"

import { useEffect, useRef } from "react"
import Link from "next/link"
import { formatDistanceToNow } from "date-fns"
import {
    AlertCircle,
    Bell,
    Check,
    CheckCheck,
    Info,
    XCircle,
    Clock,
    Settings,
    Loader2
} from "lucide-react"

import { ScrollArea } from "@/components/ui/scroll-area"
import {
    Sheet,
    SheetContent,
    SheetHeader,
    SheetTitle
} from "@/components/ui/sheet"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { useNotifications } from "@/hooks/use-notifications"
import { cn } from "@/lib/utils"

interface NotificationsDrawerProps {
    open: boolean
    onClose: () => void
}

export function NotificationsDrawer({
    open,
    onClose
}: NotificationsDrawerProps) {
    const {
        notifications,
        markAsRead,
        isLoading,
        fetchNextPage,
        isFetchingNextPage,
        hasNextPage
    } = useNotifications()

    // Sentinel for IntersectionObserver
    const observerTarget = useRef<HTMLDivElement>(null)
    // Ref to the scrollable container
    const scrollAreaRef = useRef<HTMLDivElement>(null)

    useEffect(() => {
        const rootEl = scrollAreaRef.current
        const targetEl = observerTarget.current
        if (!rootEl || !targetEl) return

        const observer = new IntersectionObserver(
            (entries) => {
                if (
                    entries[0].isIntersecting &&
                    hasNextPage &&
                    !isFetchingNextPage
                ) {
                    fetchNextPage()
                }
            },
            {
                root: rootEl,
                rootMargin: "100px 0px",
                threshold: 0.1
            }
        )

        observer.observe(targetEl)
        return () => {
            observer.disconnect()
        }
    }, [hasNextPage, isFetchingNextPage, fetchNextPage])

    const unreadCount = notifications.filter(n => !n.read_at).length

    const getIcon = (kind: string, entityType: string) => {
        if (entityType === "WORKFLOW")
            return <Settings className="h-4 w-4 text-purple-500" />
        if (entityType === "JOB")
            return <Clock className="h-4 w-4 text-blue-500" />

        switch (kind) {
            case "WEB_INFO":
                return <Info className="h-4 w-4 text-blue-500" />
            case "WEB_SUCCESS":
                return <Check className="h-4 w-4 text-green-500" />
            case "WEB_ERROR":
                return <XCircle className="h-4 w-4 text-red-500" />
            case "WEB_ALERT":
                return <AlertCircle className="h-4 w-4 text-yellow-500" />
            default:
                return <Info className="h-4 w-4 text-muted-foreground" />
        }
    }

    const getKindClass = (kind: string) => {
        switch (kind) {
            case "WEB_INFO":
                return "border-l-4 border-l-blue-500 bg-blue-500/5"
            case "WEB_SUCCESS":
                return "border-l-4 border-l-green-500 bg-green-500/5"
            case "WEB_ERROR":
                return "border-l-4 border-l-red-500 bg-red-500/5"
            case "WEB_ALERT":
                return "border-l-4 border-l-yellow-500 bg-yellow-500/5"
            default:
                return "border-l-4 border-l-muted"
        }
    }

    const handleMarkAllAsRead = () => {
        notifications.forEach(n => {
            if (!n.read_at) markAsRead?.(n.id)
        })
    }

    return (
        <Sheet open={open} onOpenChange={onClose}>
            <SheetContent className="w-full sm:max-w-md p-0 h-full">
                <div className="flex flex-col h-full">
                    <SheetHeader className="px-6 py-4 border-b flex-shrink-0">
                        <div className="flex items-center justify-between">
                            <div className="flex items-center gap-2">
                                <Bell className="h-5 w-5 text-primary" />
                                <SheetTitle>Notifications</SheetTitle>
                                {unreadCount > 0 && (
                                    <span className="text-sm text-muted-foreground">
                                        ({unreadCount})
                                    </span>
                                )}
                            </div>
                            <Button
                                variant="ghost"
                                size="icon"
                                onClick={onClose}
                                className="rounded-full"
                            >
                                <span className="sr-only">Close</span>
                            </Button>
                        </div>
                    </SheetHeader>

                    {unreadCount > 0 && (
                        <div className="px-6 py-3 border-b">
                            <Button
                                variant="outline"
                                size="sm"
                                className="w-full text-xs h-8"
                                onClick={handleMarkAllAsRead}
                            >
                                <CheckCheck className="h-3.5 w-3.5 mr-2" />
                                Mark all as read
                            </Button>
                        </div>
                    )}

                    <div className="flex-1 h-full">
                        {/* Attach ref here so observer can use it as root */}
                        <ScrollArea className="h-full" ref={scrollAreaRef}>
                            <div className="divide-y">
                                {isLoading && notifications.length === 0 ? (
                                    Array.from({ length: 5 }).map((_, i) => (
                                        <div key={i} className="px-6 py-4 space-y-3">
                                            <Skeleton className="h-4 w-3/4" />
                                            <Skeleton className="h-3 w-full" />
                                            <Skeleton className="h-3 w-1/2" />
                                        </div>
                                    ))
                                ) : notifications.length === 0 ? (
                                    <EmptyState
                                        icon={<Bell />}
                                        title="No notifications yet"
                                        message="You're all caught up! We'll notify you when there's new activity."
                                    />
                                ) : unreadCount === 0 ? (
                                    <EmptyState
                                        icon={<CheckCheck />}
                                        title="All caught up!"
                                        message="You have no unread notifications at the moment."
                                    />
                                ) : (
                                    <>
                                        {notifications
                                            .filter(n => !n.read_at)
                                            .map(notification => (
                                                <NotificationItem
                                                    key={notification.id}
                                                    notification={notification}
                                                    onMarkRead={markAsRead}
                                                    getIcon={getIcon}
                                                    getKindClass={getKindClass}
                                                />
                                            ))}

                                        {/* Observer target at bottom */}
                                        <div
                                            ref={observerTarget}
                                            className="py-4 text-center"
                                            data-testid="observer-target"
                                        >
                                            {isFetchingNextPage ? (
                                                <Loader2 className="h-5 w-5 mx-auto animate-spin text-muted-foreground" />
                                            ) : !hasNextPage ? (
                                                <span className="text-sm text-muted-foreground">
                                                    No more notifications
                                                </span>
                                            ) : (
                                                <div className="h-2" />
                                            )}
                                        </div>
                                    </>
                                )}
                            </div>
                        </ScrollArea>
                    </div>
                </div>
            </SheetContent>
        </Sheet>
    )
}

function NotificationItem({
    notification,
    onMarkRead,
    getIcon,
    getKindClass
}: {
    notification: any
    onMarkRead: (id: string) => void
    getIcon: (kind: string, entityType: string) => JSX.Element
    getKindClass: (kind: string) => string
}) {
    return (
        <div
            className={cn(
                "px-6 py-4 transition-colors group",
                notification.read_at
                    ? "bg-background hover:bg-muted/50"
                    : cn(getKindClass(notification.kind), "hover:bg-muted/30")
            )}
        >
            <div className="flex items-start gap-3">
                <div className="flex-shrink-0 mt-0.5">
                    {getIcon(notification.kind, notification.payload.entity_type)}
                </div>
                <div className="flex-1 space-y-1.5">
                    <div className="flex items-start justify-between">
                        <h4 className="font-medium text-sm">
                            {notification.payload.title}
                        </h4>
                        <span className="text-xs text-muted-foreground whitespace-nowrap">
                            {formatDistanceToNow(new Date(notification.created_at), {
                                addSuffix: true
                            })}
                        </span>
                    </div>
                    <p className="text-sm text-muted-foreground">
                        {notification.payload.message}
                    </p>
                    <div className="flex items-center gap-2 mt-2">
                        <Button
                            variant="ghost"
                            className="h-6 px-2 text-xs"
                            onClick={() => onMarkRead(notification.id)}
                        >
                            <Check className="h-3.5 w-3.5 mr-1.5" />
                            Mark as read
                        </Button>
                        {notification.payload.action_url && (
                            <Button
                                variant="outline"
                                className="h-6 px-2 text-xs"
                                asChild
                            >
                                <Link href={notification.payload.action_url}>
                                    View {notification.payload.entity_type.toLowerCase()}
                                </Link>
                            </Button>
                        )}
                    </div>
                </div>
            </div>
        </div>
    )
}

function EmptyState({
    icon,
    title,
    message
}: {
    icon: React.ReactNode
    title: string
    message: string
}) {
    return (
        <div className="flex flex-col items-center justify-center p-6 text-center">
            <div className="mb-3 text-muted-foreground/50">{icon}</div>
            <h3 className="font-medium text-muted-foreground">{title}</h3>
            <p className="text-sm text-muted-foreground/70 mt-1.5">
                {message}
            </p>
        </div>
    )
}
