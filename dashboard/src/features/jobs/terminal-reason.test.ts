import { describe, expect, it } from "vitest"

import { getTerminalReason } from "./terminal-reason"

describe("getTerminalReason", () => {
    it("returns the API-provided reason for terminal failures", () => {
        expect(getTerminalReason("FAILED", "Execution time limit exceeded"))
            .toBe("Execution time limit exceeded")
    })

    it("uses a generic fallback for missing terminal data", () => {
        expect(getTerminalReason("CANCELED", "")).toBe("Terminal reason unavailable")
    })

    it("does not expose a reason for non-terminal statuses", () => {
        expect(getTerminalReason("RUNNING", "stale retry error")).toBeUndefined()
    })
})
