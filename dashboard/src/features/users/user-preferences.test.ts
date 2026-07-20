import { describe, expect, it } from "vitest"

import { canReceiveNotifications } from "./user-preferences"
import type { User } from "./types"

const user = (notificationPreference: User["notification_preference"]): User => ({
    email: "user@example.com",
    notification_preference: notificationPreference,
    created_at: "2026-07-20T00:00:00Z",
    updated_at: "2026-07-20T00:00:00Z",
})

describe("canReceiveNotifications", () => {
    it.each(["ALL", "ALERTS"] as const)("enables %s notifications", (preference) => {
        expect(canReceiveNotifications(user(preference))).toBe(true)
    })

    it("disables notifications for NONE", () => {
        expect(canReceiveNotifications(user("NONE"))).toBe(false)
    })

    it("does not fetch before the user is available", () => {
        expect(canReceiveNotifications(undefined)).toBe(false)
    })
})
