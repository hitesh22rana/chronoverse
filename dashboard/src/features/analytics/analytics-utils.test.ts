import { describe, expect, it } from "vitest"

import {
    divide,
    formatRetainedLogsPerJob,
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

    it("formats retained-log rates as safe whole-number approximations", () => {
        expect(formatRetainedLogsPerJob(26056, 286)).toBe("~91 logs per job")
        expect(formatRetainedLogsPerJob(1, 10)).toBe("<1 log per job")
        expect(formatRetainedLogsPerJob(1, 1)).toBe("~1 log per job")
        expect(formatRetainedLogsPerJob(0, 10)).toBeNull()
        expect(formatRetainedLogsPerJob(10, 0)).toBeNull()
        expect(formatRetainedLogsPerJob(Number.NaN, 10)).toBeNull()
    })

    it("only truncates workflow labels that exceed the chart limit", () => {
        expect(truncateWorkflowLabel("short-workflow")).toBe("short-workflow")
        expect(truncateWorkflowLabel("workflow-name-that-is-too-long")).toBe("workflow-name-tha…")
    })
})
