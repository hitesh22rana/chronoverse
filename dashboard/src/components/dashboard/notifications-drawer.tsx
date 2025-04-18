"use client"

import {
    forwardRef,
    Fragment,
    useEffect,
    useRef,
    useState,
    useCallback
} from "react"
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

import { useNotifications, type Notification } from "@/hooks/use-notifications"

import { cn } from "@/lib/utils"

interface NotificationsDrawerProps {
    open: boolean
    onClose: () => void
}

export function NotificationsDrawer({ open, onClose }: NotificationsDrawerProps) {
    const {
        notifications,
        isLoading,
        markAsRead,
        fetchNextPage,
        isFetchingNextPage,
        hasNextPage
    } = useNotifications()

    const [observerInitialized, setObserverInitialized] = useState(false)
    const lastItemRef = useRef<HTMLDivElement>(null)
    const scrollContainerRef = useRef<HTMLDivElement>(null)

    // Setup intersection observer with a delay to ensure DOM is ready
    const setupObserver = useCallback(() => {
        if (!open || !scrollContainerRef.current || !lastItemRef.current) return

        const options = {
            root: scrollContainerRef.current,
            rootMargin: "200px",
            threshold: 0.1,
        }

        const observer = new IntersectionObserver((entries) => {
            const [entry] = entries
            if (entry.isIntersecting && hasNextPage && !isFetchingNextPage) {
                fetchNextPage()
            }
        }, options)

        const currentRef = lastItemRef.current
        if (currentRef) {
            observer.observe(currentRef)
            setObserverInitialized(true)
        }

        return () => {
            if (currentRef) {
                observer.unobserve(currentRef)
            }
        }
    }, [open, fetchNextPage, hasNextPage, isFetchingNextPage])

    // Initial setup with delay
    useEffect(() => {
        if (!open) return

        setObserverInitialized(false)

        const timeoutId = setTimeout(() => {
            setupObserver()
        }, 500)

        return () => {
            clearTimeout(timeoutId)
        }
    }, [open, setupObserver])

    // Re-initialize observer when notifications change
    useEffect(() => {
        if (open && notifications.length > 0 && !observerInitialized) {
            setupObserver()
        }
    }, [open, notifications.length, observerInitialized, setupObserver])

    // Force check intersection on scroll
    useEffect(() => {
        if (!open || !scrollContainerRef.current) return

        const handleScroll = () => {
            if (lastItemRef.current && hasNextPage && !isFetchingNextPage) {
                const rect = lastItemRef.current.getBoundingClientRect()
                const containerRect = scrollContainerRef.current!.getBoundingClientRect()

                const isVisible = rect.top >= containerRect.top - 200 && rect.bottom <= containerRect.bottom + 200

                if (isVisible) {
                    fetchNextPage()
                }
            }
        }

        const scrollContainer = scrollContainerRef.current
        scrollContainer.addEventListener("scroll", handleScroll)

        return () => {
            scrollContainer.removeEventListener("scroll", handleScroll)
        }
    }, [open, hasNextPage, isFetchingNextPage, fetchNextPage])

    const getIcon = (kind: string, entityType: string) => {
        if (entityType === "WORKFLOW") return <Settings className="h-4 w-4 text-purple-500" />
        if (entityType === "JOB") return <Clock className="h-4 w-4 text-blue-500" />

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

    // Event delegation handler for notification actions
    const handleNotificationAction = (e: React.MouseEvent) => {
        // Find the closest button with data-action attribute
        const button = (e.target as HTMLElement).closest('button[data-action]');

        if (!button) return;

        const action = button.getAttribute('data-action');
        const notificationId = button.getAttribute('data-notification-id');

        if (action === 'mark-read' && notificationId) {
            e.preventDefault();
            markAsRead?.([notificationId]);
        }
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
                            </div>
                            <Button variant="ghost" size="icon" onClick={onClose} className="rounded-full">
                                <span className="sr-only">Close</span>
                            </Button>
                        </div>
                    </SheetHeader>

                    {notifications.length > 0 && (
                        <div className="px-6 py-3 border-b">
                            <Button variant="outline" size="sm" className="w-full text-xs h-8" onClick={() => markAsRead?.(notifications.map(n => n.id))}>
                                <CheckCheck className="h-3.5 w-3.5 mr-2" />
                                Mark all as read
                            </Button>
                        </div>
                    )}

                    {/* Add the click handler to the container for event delegation */}
                    <ScrollArea
                        ref={scrollContainerRef}
                        className="flex-1 h-full overflow-auto"
                        onClick={handleNotificationAction}
                    >
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
                        ) : (
                            <Fragment>
                                {notifications.map((notification, index) => (
                                    <NotificationItem
                                        key={notification.id}
                                        notification={notification}
                                        getIcon={getIcon}
                                        getKindClass={getKindClass}
                                        className={index & 1 ? "bg-background/50" : ""}
                                        ref={index === notifications.length - 1 ? lastItemRef : undefined}
                                    />
                                ))}

                                {/* Loading indicator */}
                                {isFetchingNextPage && (
                                    <div className="p-4 flex justify-center">
                                        <Loader2 className="h-4 w-4 animate-spin" />
                                    </div>
                                )}

                                {/* Empty state when no more notifications */}
                                {!hasNextPage && (
                                    <EmptyState
                                        icon={<Check />}
                                        title="No more notifications"
                                        message="You've reached the end of your notifications."
                                    />
                                )}
                            </Fragment>
                        )}
                    </ScrollArea>
                </div>
            </SheetContent>
        </Sheet>
    )
}

// Modified to remove the onMarkRead prop and use data attributes instead
const NotificationItem = forwardRef<
    HTMLDivElement,
    {
        notification: Notification
        getIcon: (kind: string, entityType: string) => React.ReactNode
        getKindClass: (kind: string) => string
        className?: string
    }
>(({ notification, getIcon, getKindClass, className }, ref) => {
    return (
        <div
            ref={ref}
            className={cn(
                "px-6 py-4 transition-colors group",
                notification.read_at
                    ? "bg-background hover:bg-muted/50"
                    : cn(getKindClass(notification.kind), "hover:bg-muted/30"),
                className
            )}
        >
            <div className="flex items-start gap-3">
                <div className="flex-shrink-0 mt-0.5">{getIcon(notification.kind, notification.payload.entity_type)}</div>
                <div className="flex-1 space-y-1.5">
                    <div className="flex items-start justify-between">
                        <h4 className="font-medium text-sm">{notification.payload.title}</h4>
                        <span className="text-xs text-muted-foreground whitespace-nowrap">
                            {formatDistanceToNow(new Date(notification.created_at), {
                                addSuffix: true,
                            })}
                        </span>
                    </div>
                    <p className="text-sm text-muted-foreground">{notification.payload.message}</p>
                    <div className="flex items-center gap-2 mt-2">
                        {!notification.read_at && (
                            <Button
                                variant="ghost"
                                className="h-6 px-2 text-xs"
                                data-action="mark-read"
                                data-notification-id={notification.id}
                            >
                                <Check className="h-3.5 w-3.5 mr-1.5" />
                                Mark as read
                            </Button>
                        )}
                        {notification.payload.action_url && (
                            <Button variant="outline" className="h-6 px-2 text-xs" asChild>
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
})

NotificationItem.displayName = "NotificationItem"

function EmptyState({
    icon,
    title,
    message,
}: {
    icon: React.ReactNode
    title: string
    message: string
}) {
    return (
        <div className="flex flex-col items-center justify-center p-6 text-center">
            <div className="mb-3 text-muted-foreground/50">{icon}</div>
            <h3 className="font-medium text-muted-foreground">{title}</h3>
            <p className="text-sm text-muted-foreground/70 mt-1.5">{message}</p>
        </div>
    )
}