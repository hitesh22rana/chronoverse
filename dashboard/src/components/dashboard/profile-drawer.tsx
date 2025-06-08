"use client"

import { useMemo, useState } from "react"
import { toast } from "sonner"
import { formatDistanceToNow } from "date-fns"
import {
    Bell,
    Clock,
    LogOut,
    Mail,
    User
} from "lucide-react"

import {
    Sheet,
    SheetContent,
    SheetHeader,
    SheetTitle,
    SheetFooter
} from "@/components/ui/sheet"
import {
    Card,
    CardContent,
    CardDescription,
    CardHeader,
    CardTitle
} from "@/components/ui/card"
import { Label } from "@/components/ui/label"
import {
    Select,
    SelectContent,
    SelectItem,
    SelectTrigger,
    SelectValue
} from "@/components/ui/select"
import { Separator } from "@/components/ui/separator"
import {
    Avatar,
    AvatarFallback,
    AvatarImage
} from "@/components/ui/avatar"
import { Badge } from "@/components/ui/badge"
import {
    Dialog,
    DialogContent,
    DialogDescription,
    DialogFooter,
    DialogHeader,
    DialogTitle
} from "@/components/ui/dialog"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"

import { useUsers } from "@/hooks/use-users"
import { useAuth } from "@/hooks/use-auth"
import { useNotifications } from "@/hooks/use-notifications"


interface ProfileDrawerProps {
    open: boolean;
    onClose: () => void;
}

export function ProfileDrawer({ open, onClose }: ProfileDrawerProps) {
    const { user, updateUser, isUpdating } = useUsers()
    const { logout, isLogoutLoading } = useAuth()
    const { refetch: refetchNotifications } = useNotifications()

    // State for password confirmation dialog
    const [confirmDialogOpen, setConfirmDialogOpen] = useState(false)
    const [password, setPassword] = useState("")
    const [newPreference, setNewPreference] = useState("")

    const initials = useMemo(() => {
        if (!user?.email) return "U"
        return user.email
            .split('@')[0]
            .split('.')
            .map(part => part[0])
            .join('')
            .toUpperCase()
            .slice(0, 2)
    }, [user?.email])

    // Handler for notification preference change
    const handlePreferenceChange = (value: string) => {
        if (value === user?.notification_preference) return

        setNewPreference(value)
        setConfirmDialogOpen(true)
    }

    // Handler for password confirmation and update
    const handleConfirmUpdate = () => {
        if (!password.trim()) {
            toast.error("Password is required")
            return
        }

        updateUser({
            password,
            notification_preference: newPreference
        }, {
            onSuccess: () => {
                setConfirmDialogOpen(false)
                setPassword("")
                refetchNotifications()
            }
        })
    }

    // Handler for sign out
    const handleSignOut = () => {
        logout()
    }

    if (!user) return null

    return (
        <>
            <Sheet open={open} onOpenChange={onClose}>
                <SheetContent className="w-full sm:max-w-md p-0 gap-0 h-full flex flex-col">
                    {/* Header */}
                    <SheetHeader className="px-6 py-4 border-b flex-shrink-0">
                        <div className="flex items-center gap-2">
                            <User className="h-5 w-5" />
                            <SheetTitle>Profile</SheetTitle>
                        </div>
                    </SheetHeader>

                    {/* Content */}
                    <div className="flex-1 overflow-auto px-6 py-6 space-y-6">
                        {/* User Identity */}
                        <div className="flex items-center gap-4">
                            <Avatar className="h-20 w-20">
                                <AvatarImage
                                    src="/assets/avatar.svg"
                                    alt="User Avatar"
                                    className="object-cover"
                                />
                                <AvatarFallback className="text-lg">{initials}</AvatarFallback>
                            </Avatar>
                            <div>
                                <h2 className="text-xl font-semibold">{user.email.split('@')[0]}</h2>
                                <div className="flex items-center text-muted-foreground gap-1 mt-1">
                                    <Mail className="h-3.5 w-3.5" />
                                    <span className="text-sm">{user.email}</span>
                                </div>
                                <Badge variant="secondary" className="mt-2">
                                    <Clock className="h-3 w-3 mr-1" />
                                    Joined {formatDistanceToNow(new Date(user.created_at), { addSuffix: true })}
                                </Badge>
                            </div>
                        </div>

                        <Separator />

                        {/* Notification Preferences and Details */}
                        <Card>
                            <CardHeader className="px-4 -my-2">
                                <CardTitle className="flex items-center text-base">
                                    <Bell className="h-4 w-4 mr-2" />
                                    Notification Settings
                                </CardTitle>
                                <CardDescription>
                                    Configure how you receive notifications
                                </CardDescription>
                            </CardHeader>
                            <CardContent className="px-4 -my-2">
                                <div className="grid gap-4">
                                    <Label htmlFor="notification-preference">Notification Preference</Label>
                                    <Select
                                        defaultValue={user.notification_preference}
                                        onValueChange={handlePreferenceChange}
                                    >
                                        <SelectTrigger id="notification-preference">
                                            <SelectValue placeholder="Select preference" />
                                        </SelectTrigger>
                                        <SelectContent>
                                            <SelectItem value="ALL">All notifications</SelectItem>
                                            <SelectItem value="ALERTS">Important only</SelectItem>
                                            <SelectItem value="NONE">No notifications</SelectItem>
                                        </SelectContent>
                                    </Select>
                                    <span className="md:text-sm text-xs text-muted-foreground ml-auto"> Last updated {formatDistanceToNow(new Date(user.updated_at), { addSuffix: true })}</span>
                                </div>
                            </CardContent>
                        </Card>
                    </div>

                    {/* Sticky Sign Out Footer */}
                    <SheetFooter className="px-6 py-4 border-t mt-auto">
                        <Button
                            type="button"
                            className="w-full flex items-center gap-2 py-5 cursor-pointer"
                            onClick={handleSignOut}
                            disabled={isLogoutLoading}
                        >
                            <LogOut className="h-4 w-4" />
                            {isLogoutLoading ? "Signing out..." : "Sign Out"}
                        </Button>
                    </SheetFooter>
                </SheetContent>
            </Sheet>

            {/* Password Confirmation Dialog */}
            <Dialog open={confirmDialogOpen} onOpenChange={setConfirmDialogOpen}>
                <DialogContent className="sm:max-w-md">
                    <DialogHeader>
                        <DialogTitle>Confirm Password</DialogTitle>
                        <DialogDescription>
                            Please enter your password to change notification preferences
                        </DialogDescription>
                    </DialogHeader>
                    <div className="space-y-4 py-4">
                        <div className="space-y-2">
                            <Label htmlFor="password">Password</Label>
                            <Input
                                id="password"
                                type="password"
                                placeholder="Enter your password"
                                value={password}
                                onChange={(e) => setPassword(e.target.value)}
                            />
                        </div>
                    </div>
                    <DialogFooter className="grid grid-cols-1 sm:grid-cols-2 gap-4">
                        <Button
                            type="button"
                            variant="outline"
                            onClick={() => {
                                setConfirmDialogOpen(false)
                                setPassword("")
                            }}
                            disabled={isUpdating}
                            className="cursor-pointer w-full"
                        >
                            Cancel
                        </Button>
                        <Button
                            type="button"
                            onClick={handleConfirmUpdate}
                            disabled={!password || isUpdating}
                            className="cursor-pointer w-full"
                        >
                            {isUpdating ? "Updating..." : "Confirm"}
                        </Button>
                    </DialogFooter>
                </DialogContent>
            </Dialog>
        </>
    )
}