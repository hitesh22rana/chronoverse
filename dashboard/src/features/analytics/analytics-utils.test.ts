import { describe, expect, it } from "vitest"

import {
    divide,
    formatWorkflowKind,
    truncateWorkflowLabel,
} from "./analytics-utils"

describe("analytics utilities", () => {
    it("divides aggregate values without returning an invalid number", () => {
        expect(divide(15, 3)).toBe(5)
        expect(divide(15, 0)).toBe(0)
        expect(divide()).toBe(0)
    })

    it("turns workflow kinds into readable labels", () => {
        expect(formatWorkflowKind("CONTAINER")).toBe("Container")
        expect(formatWorkflowKind("HTTP_HEARTBEAT")).toBe("Http Heartbeat")
    })

    it("only truncates workflow labels that exceed the chart limit", () => {
        expect(truncateWorkflowLabel("short-workflow")).toBe("short-workflow")
        expect(truncateWorkflowLabel("workflow-name-that-is-too-long")).toBe("workflow-name-tha…")
    })
})
