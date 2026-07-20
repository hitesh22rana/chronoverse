import { describe, expect, it } from "vitest"

import {
    isDisallowedIntervalKey,
    isIntervalRangeInvalid,
    normalizeIntervalFilter,
} from "./interval-filter"

describe("workflow interval filters", () => {
    it("accepts positive whole-minute values within the workflow interval range", () => {
        expect(normalizeIntervalFilter("1")).toBe("1")
        expect(normalizeIntervalFilter("5")).toBe("5")
        expect(normalizeIntervalFilter("010080")).toBe("10080")
    })

    it("rejects empty, negative, fractional, non-numeric, and out-of-range values", () => {
        expect(normalizeIntervalFilter()).toBe("")
        expect(normalizeIntervalFilter("")).toBe("")
        expect(normalizeIntervalFilter("0")).toBe("")
        expect(normalizeIntervalFilter("-1")).toBe("")
        expect(normalizeIntervalFilter("1.5")).toBe("")
        expect(normalizeIntervalFilter("five")).toBe("")
        expect(normalizeIntervalFilter("10081")).toBe("")
    })

    it("only rejects a range when both valid bounds are present and reversed", () => {
        expect(isIntervalRangeInvalid("5", "3")).toBe(true)
        expect(isIntervalRangeInvalid("5", "5")).toBe(false)
        expect(isIntervalRangeInvalid("5", "")).toBe(false)
        expect(isIntervalRangeInvalid("", "5")).toBe(false)
    })

    it("blocks number-input keys that can produce non-positive or fractional values", () => {
        for (const key of ["-", "+", ".", "e", "E"]) {
            expect(isDisallowedIntervalKey(key)).toBe(true)
        }
        expect(isDisallowedIntervalKey("5")).toBe(false)
        expect(isDisallowedIntervalKey("Backspace")).toBe(false)
    })
})
