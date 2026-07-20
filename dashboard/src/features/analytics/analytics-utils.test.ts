import { describe, expect, it } from "vitest"

import {
    divide,
    formatJobsPerWorkflow,
    formatLogsPerJob,
    formatWorkflowKind,
    truncateWorkflowLabel,
    formatInteger,
    withTerminalJobActivity,
} from "./analytics-utils"

describe("analytics utilities", () => {
    it("divides aggregate values without returning an invalid number", () => {
        expect(divide(15, 3)).toBe(5)
        expect(divide(15, 0)).toBe(0)
        expect(divide()).toBe(0)
    })

    it("formats omitted or invalid counters as zero", () => {
        expect(formatInteger()).toBe("0")
        expect(formatInteger(Number.NaN)).toBe("0")
        expect(formatInteger(1000)).toBe("1,000")
    })

    it("turns workflow kinds into readable labels", () => {
        expect(formatWorkflowKind("CONTAINER")).toBe("Container")
        expect(formatWorkflowKind("HTTP_HEARTBEAT")).toBe("Http Heartbeat")
        expect(formatWorkflowKind("IMAGE")).toBe("Image")
    })

    it("only includes workflow kinds with terminal-job activity", () => {
        const workflowKinds = [
            { kind: "HEARTBEAT", total_jobs: 0 },
            { kind: "CONTAINER", total_jobs: 2 },
        ]

        expect(withTerminalJobActivity(workflowKinds)).toEqual([
            { kind: "CONTAINER", total_jobs: 2 },
        ])
        expect(withTerminalJobActivity([{ kind: "HEARTBEAT", total_jobs: 0 }])).toEqual([])
    })

    it("formats generated-log rates as safe whole-number approximations", () => {
        expect(formatLogsPerJob(26056, 286)).toBe("~91 logs per job")
        expect(formatLogsPerJob(1, 10)).toBe("<1 log per job")
        expect(formatLogsPerJob(1, 1)).toBe("~1 log per job")
        expect(formatLogsPerJob(0, 10)).toBeNull()
        expect(formatLogsPerJob(10, 0)).toBeNull()
        expect(formatLogsPerJob(Number.NaN, 10)).toBeNull()
    })

    it("formats jobs per workflow as a safe whole-number approximation", () => {
        expect(formatJobsPerWorkflow(286, 19)).toBe("~15 jobs per workflow")
        expect(formatJobsPerWorkflow(1, 10)).toBe("<1 job per workflow")
        expect(formatJobsPerWorkflow(1, 1)).toBe("~1 job per workflow")
        expect(formatJobsPerWorkflow(0, 10)).toBeNull()
        expect(formatJobsPerWorkflow(10, 0)).toBeNull()
        expect(formatJobsPerWorkflow(Number.NaN, 10)).toBeNull()
    })

    it("only truncates workflow labels that exceed the chart limit", () => {
        expect(truncateWorkflowLabel("short-workflow")).toBe("short-workflow")
        expect(truncateWorkflowLabel("workflow-name-that-is-too-long")).toBe("workflow-name-tha…")
    })
})
