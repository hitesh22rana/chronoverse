import { describe, expect, it } from "vitest"

import { getStatusLabel, getStatusMeta, normalizeStatus } from "./job-status"

describe("job status", () => {
    it.each([
        [" started ", "RUNNING"],
        ["BUILDING", "RUNNING"],
        ["cancelled", "CANCELED"],
        ["active", "COMPLETED"],
        ["terminated", "TERMINATED"],
    ] as const)("normalizes %s to %s", (input, expected) => {
        expect(normalizeStatus(input)).toBe(expected)
    })

    it.each([undefined, null, "", "unexpected"])("falls back to UNKNOWN for %s", (input) => {
        expect(normalizeStatus(input)).toBe("UNKNOWN")
        expect(getStatusMeta(input).key).toBe("UNKNOWN")
    })

    it("uses workflow-specific lifecycle labels", () => {
        expect(getStatusLabel("RUNNING", "workflow")).toBe("Building")
        expect(getStatusLabel("COMPLETED", "workflow")).toBe("Active")
    })

    it("keeps job and default labels when no specialized label exists", () => {
        expect(getStatusLabel("RUNNING", "job")).toBe("Running")
        expect(getStatusLabel("FAILED", "workflow")).toBe("Failed")
    })

    it("exposes animation metadata only for running states", () => {
        expect(getStatusMeta("RUNNING").iconClass).toBe("animate-spin")
        expect(getStatusMeta("COMPLETED").iconClass).toBeUndefined()
    })
})
