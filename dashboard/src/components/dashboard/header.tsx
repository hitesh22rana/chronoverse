"use client"

import { Bell } from "lucide-react"
import { useNotifications } from "@/hooks/use-notifications"

import { Button } from "@/components/ui/button"
import { Badge } from "@/components/ui/badge"
import { UserNav } from "@/components/dashboard/user-nav"
import { ThemeToggle } from "@/components/theme-toggle"

interface HeaderProps {
    onNotificationsClick: () => void
}

export function Header({ onNotificationsClick }: HeaderProps) {
    const { unreadCount = 0 } = useNotifications()

    return (
        <header className="flex h-16 items-center gap-4 border-b md:px-6 px-4">
            <h1 className="text-2xl md:text-3xl bg-clip-text text-transparent text-center bg-gradient-to-b from-neutral-900 to-neutral-700 dark:from-neutral-600 dark:to-white font-sans py-2 md:py-10 relative z-20 font-bold tracking-tight">Chronoverse</h1>
            <div className="ml-auto flex items-center gap-2">
                <ThemeToggle />
                <Button
                    variant="ghost"
                    size="icon"
                    className="relative rounded-full"
                    onClick={onNotificationsClick}
                >
                    <Bell className="h-5 w-5" />
                    {unreadCount > 0 && (
                        <Badge
                            variant="destructive"
                            className="absolute right-0 top-0 size-4 rounded-full p-0 flex items-center justify-center text-xs overflow-visible"
                        >
                            {unreadCount > 9 ?
                                <span className="absolute -top-0 -right-0.5">
                                    9+
                                </span>
                                : (
                                    <span>
                                        {unreadCount}
                                    </span>
                                )}
                        </Badge>
                    )}
                    <span className="sr-only">Notifications</span>
                </Button>
                <UserNav />
            </div>
        </header>
    )
}