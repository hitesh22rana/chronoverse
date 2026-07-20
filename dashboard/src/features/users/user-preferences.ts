import type { User } from "@/features/users/types"

export const canReceiveNotifications = (user?: User): boolean =>
    Boolean(user && user.notification_preference !== "NONE")
