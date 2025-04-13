"use client"

import { useState, useEffect, useRef } from "react"
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
import {
    Tabs,
    TabsContent,
    TabsList,
    TabsTrigger
} from "@/components/ui/tabs"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"

import { useNotifications } from "@/hooks/use-notifications"

import { cn } from "@/lib/utils"

interface NotificationsDrawerProps {
    open: boolean
    onClose: () => void
}

export function NotificationsDrawer({ open, onClose }: NotificationsDrawerProps) {
    const { notifications, markAsRead, isLoading, cursor, fetchNextPage, isFetchingNextPage } = useNotifications()
    const [activeTab, setActiveTab] = useState("all")

    // Element to observe for intersection (infinite scroll)
    const observerTarget = useRef<HTMLDivElement>(null)

    // Set up intersection observer for infinite scroll
    useEffect(() => {
        const observer = new IntersectionObserver(
            (entries) => {
                if (entries[0].isIntersecting && cursor && !isLoading && !isFetchingNextPage) {
                    fetchNextPage()
                }
            },
            { threshold: 0.1 }
        )

        const currentTarget = observerTarget.current
        if (currentTarget) {
            observer.observe(currentTarget)
        }

        return () => {
            if (currentTarget) {
                observer.unobserve(currentTarget)
            }
        }
    }, [cursor, isLoading, isFetchingNextPage, fetchNextPage])

    const unreadCount = notifications?.filter(n => !n.readAt).length

    const notificationsList = activeTab === "unread"
        ? notifications.filter(n => !n.readAt)
        : notifications

    const getIcon = (kind: string, entityType: string) => {
        // First check entity type for icon
        if (entityType === "WORKFLOW") {
            return <Settings className="h-4 w-4 text-purple-500" />
        }

        if (entityType === "JOB") {
            return <Clock className="h-4 w-4 text-blue-500" />
        }

        // Fallback to notification kind
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

    const handleMarkAsRead = (id: string) => {
        markAsRead?.(id)
    }

    const handleMarkAllAsRead = () => {
        notifications.forEach(notification => {
            if (!notification.readAt) {
                markAsRead?.(notification.id)
            }
        })
    }

    return (
        <Sheet open={open} onOpenChange={onClose}>
            <SheetContent className="w-full sm:max-w-md p-0 overflow-hidden">
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

                        <div className="flex items-center justify-between mt-2">
                            <Tabs defaultValue="all" value={activeTab} onValueChange={setActiveTab} className="w-full">
                                <TabsList className="grid grid-cols-2">
                                    <TabsTrigger value="all" className="rounded-md">
                                        All
                                    </TabsTrigger>
                                    <TabsTrigger value="unread" className="rounded-md">
                                        Unread
                                    </TabsTrigger>
                                </TabsList>

                                {unreadCount > 0 && (
                                    <div className="px-6 py-2 border-b bg-muted/30 mt-2">
                                        <Button
                                            variant="ghost"
                                            size="sm"
                                            className="w-full text-xs flex items-center justify-center gap-1 hover:bg-background"
                                            onClick={handleMarkAllAsRead}
                                        >
                                            <CheckCheck className="h-3.5 w-3.5" />
                                            <span>Mark all as read</span>
                                        </Button>
                                    </div>
                                )}

                                <TabsContent value="all" className="mt-0">
                                    <ScrollArea className="h-[calc(100vh-175px)]">
                                        {isLoading && notificationsList.length === 0 ? (
                                            <div className="flex flex-col gap-4 p-6">
                                                {[...Array(5)].map((_, i) => (
                                                    <div key={i} className="flex flex-col gap-2">
                                                        <div className="flex items-center justify-between">
                                                            <Skeleton className="h-5 w-40" />
                                                            <Skeleton className="h-4 w-24" />
                                                        </div>
                                                        <Skeleton className="h-4 w-full" />
                                                        <Skeleton className="h-4 w-3/4" />
                                                    </div>
                                                ))}
                                            </div>
                                        ) : notificationsList?.length === 0 ? (
                                            <div className="flex h-full flex-col items-center justify-center p-6 text-center">
                                                <Bell className="h-10 w-10 text-muted-foreground/50 mb-3" />
                                                <h3 className="font-medium text-muted-foreground">No notifications yet</h3>
                                                <p className="text-sm text-muted-foreground/70 mt-1">
                                                    You&apos;re all caught up! We&apos;ll notify you when there&apos;s new activity.
                                                </p>
                                            </div>
                                        ) : (
                                            <div className="divide-y">
                                                {notificationsList?.map(notification => (
                                                    <div
                                                        key={notification.id}
                                                        className={cn(
                                                            "px-6 py-4 transition-colors relative",
                                                            notification.readAt ? "bg-background" : getKindClass(notification.kind)
                                                        )}
                                                    >
                                                        <div className="flex items-start gap-3">
                                                            <div className="flex-shrink-0 mt-0.5">
                                                                {getIcon(notification.kind, notification.payload.entity_type)}
                                                            </div>
                                                            <div className="flex-1 space-y-1">
                                                                <div className="flex items-start justify-between">
                                                                    <h4 className="font-medium text-sm">{notification.payload.title}</h4>
                                                                    <span className="text-xs text-muted-foreground whitespace-nowrap ml-2">
                                                                        {formatDistanceToNow(new Date(notification?.createdAt), { addSuffix: true })}
                                                                    </span>
                                                                </div>
                                                                <p className="text-sm text-muted-foreground leading-relaxed">{notification.payload.message}</p>
                                                                <div className="flex items-center justify-between mt-1">
                                                                    {!notification.readAt && (
                                                                        <Button
                                                                            variant="ghost"
                                                                            size="sm"
                                                                            className="h-7 text-xs px-2 py-0 text-primary hover:bg-primary/5 hover:text-primary/90"
                                                                            onClick={() => handleMarkAsRead(notification.id)}
                                                                        >
                                                                            <Check className="h-3 w-3 mr-1" />
                                                                            Mark as read
                                                                        </Button>
                                                                    )}

                                                                    {notification.payload.action_url && (
                                                                        <Button
                                                                            variant="ghost"
                                                                            size="sm"
                                                                            className="h-7 text-xs px-2 py-0 ml-auto"
                                                                            asChild
                                                                        >
                                                                            <Link href={notification.payload.action_url}>
                                                                                View {notification.payload.entity_type === "WORKFLOW" ? "Workflow" : "Job"}
                                                                            </Link>
                                                                        </Button>
                                                                    )}
                                                                </div>
                                                            </div>
                                                        </div>
                                                    </div>
                                                ))}

                                                {/* Load more trigger element */}
                                                <div
                                                    ref={observerTarget}
                                                    className="py-4 flex items-center justify-center"
                                                >
                                                    {isFetchingNextPage ? (
                                                        <div className="flex items-center text-sm text-muted-foreground">
                                                            <Loader2 className="h-4 w-4 mr-2 animate-spin" />
                                                            Loading more...
                                                        </div>
                                                    ) : cursor ? (
                                                        <div className="h-8" />
                                                    ) : (
                                                        <div className="text-xs text-muted-foreground py-2">
                                                            No more notifications
                                                        </div>
                                                    )}
                                                </div>
                                            </div>
                                        )}
                                    </ScrollArea>
                                </TabsContent>

                                <TabsContent value="unread" className="mt-0">
                                    <ScrollArea className="h-[calc(100vh-175px)]">
                                        {isLoading && notificationsList.length === 0 ? (
                                            <div className="flex flex-col gap-4 p-6">
                                                {[...Array(3)].map((_, i) => (
                                                    <div key={i} className="flex flex-col gap-2">
                                                        <div className="flex items-center justify-between">
                                                            <Skeleton className="h-5 w-40" />
                                                            <Skeleton className="h-4 w-24" />
                                                        </div>
                                                        <Skeleton className="h-4 w-full" />
                                                        <Skeleton className="h-4 w-3/4" />
                                                    </div>
                                                ))}
                                            </div>
                                        ) : notificationsList?.length === 0 ? (
                                            <div className="flex h-full flex-col items-center justify-center p-6 text-center">
                                                <CheckCheck className="h-10 w-10 text-muted-foreground/50 mb-3" />
                                                <h3 className="font-medium text-muted-foreground">All caught up!</h3>
                                                <p className="text-sm text-muted-foreground/70 mt-1">
                                                    You have no unread notifications at the moment.
                                                </p>
                                            </div>
                                        ) : (
                                            <div className="divide-y">
                                                {notificationsList?.map(notification => (
                                                    <div
                                                        key={notification.id}
                                                        className={cn(
                                                            "px-6 py-4 transition-colors",
                                                            getKindClass(notification.kind)
                                                        )}
                                                    >
                                                        <div className="flex items-start gap-3">
                                                            <div className="flex-shrink-0 mt-0.5">
                                                                {getIcon(notification.kind, notification.payload.entity_type)}
                                                            </div>
                                                            <div className="flex-1 space-y-1">
                                                                <div className="flex items-start justify-between">
                                                                    <h4 className="font-medium text-sm">{notification.payload.title}</h4>
                                                                    <span className="text-xs text-muted-foreground whitespace-nowrap ml-2">
                                                                        {formatDistanceToNow(new Date(notification?.createdAt), { addSuffix: true })}
                                                                    </span>
                                                                </div>
                                                                <p className="text-sm text-muted-foreground leading-relaxed">{notification.payload.message}</p>
                                                                <div className="flex items-center justify-between mt-1">
                                                                    <Button
                                                                        variant="ghost"
                                                                        size="sm"
                                                                        className="h-7 text-xs px-2 py-0 text-primary hover:bg-primary/5 hover:text-primary/90"
                                                                        onClick={() => handleMarkAsRead(notification.id)}
                                                                    >
                                                                        <Check className="h-3 w-3 mr-1" />
                                                                        Mark as read
                                                                    </Button>

                                                                    {notification.payload.action_url && (
                                                                        <Button
                                                                            variant="ghost"
                                                                            size="sm"
                                                                            className="h-7 text-xs px-2 py-0"
                                                                            asChild
                                                                        >
                                                                            <Link href={notification.payload.action_url}>
                                                                                View {notification.payload.entity_type === "WORKFLOW" ? "Workflow" : "Job"}
                                                                            </Link>
                                                                        </Button>
                                                                    )}
                                                                </div>
                                                            </div>
                                                        </div>
                                                    </div>
                                                ))}

                                                {/* Load more trigger element */}
                                                {activeTab === "unread" && unreadCount > 0 && (
                                                    <div
                                                        ref={observerTarget}
                                                        className="py-4 flex items-center justify-center"
                                                    >
                                                        {isFetchingNextPage ? (
                                                            <div className="flex items-center text-sm text-muted-foreground">
                                                                <Loader2 className="h-4 w-4 mr-2 animate-spin" />
                                                                Loading more...
                                                            </div>
                                                        ) : cursor ? (
                                                            <div className="h-8" />
                                                        ) : (
                                                            <div className="text-xs text-muted-foreground py-2">
                                                                No more notifications
                                                            </div>
                                                        )}
                                                    </div>
                                                )}
                                            </div>
                                        )}
                                    </ScrollArea>
                                </TabsContent>
                            </Tabs>
                        </div>
                    </SheetHeader>
                </div>
            </SheetContent>
        </Sheet>
    )
}