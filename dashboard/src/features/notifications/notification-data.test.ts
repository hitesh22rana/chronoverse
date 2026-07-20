import { describe, expect, it } from "vitest"

import {
    batchNotificationIds,
    flattenNotifications,
    removeNotificationsFromPages,
} from "./notification-data"
import type { Notification, NotificationsResponse } from "./types"

const notification = (id: string): Notification => ({
    id,
    kind: "WORKFLOW",
    payload: "{}",
    created_at: "2026-07-20T00:00:00Z",
    updated_at: "2026-07-20T00:00:00Z",
})

const pages: NotificationsResponse[] = [
    { notifications: [notification("one"), notification("two")], cursor: "next" },
    { notifications: [notification("three")] },
]

describe("notification data", () => {
    it("batches mark-as-read IDs without dropping order", () => {
        expect(batchNotificationIds(["one", "two", "three", "four", "five"], 2)).toEqual([
            ["one", "two"],
            ["three", "four"],
            ["five"],
        ])
    })

    it("returns no batches for an empty selection", () => {
        expect(batchNotificationIds([])).toEqual([])
    })

    it.each([0, -1, 1.5])("rejects invalid batch size %s", (batchSize) => {
        expect(() => batchNotificationIds(["one"], batchSize)).toThrow(RangeError)
    })

    it("flattens paginated notifications", () => {
        expect(flattenNotifications(pages).map(({ id }) => id)).toEqual(["one", "two", "three"])
        expect(flattenNotifications()).toEqual([])
    })

    it("removes read notifications while preserving page metadata", () => {
        const updatedPages = removeNotificationsFromPages(pages, ["two", "three"])

        expect(updatedPages[0]?.cursor).toBe("next")
        expect(flattenNotifications(updatedPages).map(({ id }) => id)).toEqual(["one"])
        expect(pages[0]?.notifications).toHaveLength(2)
    })
})
