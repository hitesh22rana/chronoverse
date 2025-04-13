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
    const { unreadCount = 0 } = useNotifications?.() || {}

    return (
        <header className="flex h-16 items-center gap-4 border-b px-6">
            <h1 className="text-2xl md:text-3xl bg-clip-text text-transparent text-center bg-gradient-to-b from-neutral-900 to-neutral-700 dark:from-neutral-600 dark:to-white font-sans py-2 md:py-10 relative z-20 font-bold tracking-tight">Chronoverse</h1>
            <div className="ml-auto flex items-center gap-2">
                <ThemeToggle />
                <Button
                    variant="ghost"
                    size="icon"
                    className="relative"
                    onClick={onNotificationsClick}
                >
                    <Bell className="h-5 w-5" />
                    {unreadCount > 0 && (
                        <Badge
                            variant="destructive"
                            className="absolute -right-1 -top-1 h-5 w-5 p-0 flex items-center justify-center text-xs"
                        >
                            {unreadCount > 9 ? '9+' : unreadCount}
                        </Badge>
                    )}
                    <span className="sr-only">Notifications</span>
                </Button>
                <UserNav />
            </div>
        </header>
    )
}