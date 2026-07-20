import type { Notification, NotificationsResponse } from "@/features/notifications/types"

export const NOTIFICATION_BATCH_SIZE = 100

export const batchNotificationIds = (
    ids: string[],
    batchSize = NOTIFICATION_BATCH_SIZE,
): string[][] => {
    if (!Number.isInteger(batchSize) || batchSize <= 0) {
        throw new RangeError("batch size must be a positive integer")
    }

    const batches: string[][] = []
    for (let index = 0; index < ids.length; index += batchSize) {
        batches.push(ids.slice(index, index + batchSize))
    }
    return batches
}

export const flattenNotifications = (pages?: NotificationsResponse[]): Notification[] =>
    pages?.flatMap((page) => page.notifications ?? []) ?? []

export const removeNotificationsFromPages = (
    pages: NotificationsResponse[],
    ids: Iterable<string>,
): NotificationsResponse[] => {
    const removedIds = new Set(ids)
    return pages.map((page) => ({
        ...page,
        notifications: page.notifications.filter((notification) => !removedIds.has(notification.id)),
    }))
}
