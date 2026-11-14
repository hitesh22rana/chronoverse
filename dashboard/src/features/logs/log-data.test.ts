import { describe, expect, it } from "vitest"

import {
    getDownloadFilename,
    logsFromPages,
    mergeLiveLogs,
    normalizeJobLog,
} from "./log-data"
import type { JobLog } from "./types"

const log = (sequence: number, message = `message-${sequence}`): JobLog => ({
    event_id: `job:${sequence}`,
    timestamp: `2026-07-20T00:00:0${sequence}Z`,
    message,
    sequence_num: sequence,
    stream: "stdout",
})

describe("log data", () => {
    it("uses the API event identity and explicit sequence", () => {
        expect(normalizeJobLog({event_id: "event:7", sequence_num: 7, stream: "stderr"}, "highlight")).toEqual({
            event_id: "event:7", sequence_num: 7, stream: "stderr", timestamp: "", message: "", highlightToken: "highlight",
        })
        expect(normalizeJobLog({event_id: "event:7"}).sequence_num).toBe(0)
    })

    it.each([undefined, ""])("rejects a missing event identity: %j", (event_id) => {
        expect(() => normalizeJobLog({event_id})).toThrow("Log event identity is required")
    })

    it.each([NaN, Infinity, -1, 2.5, 4294967296])("rejects an invalid uint32 sequence: %j", (sequence_num) => {
        expect(() => normalizeJobLog({event_id: "event", sequence_num})).toThrow("Log sequence number must be a uint32")
    })

    it("orders equal sequences by stream and event identity regardless of arrival order", () => {
        const stderr = { ...log(7), event_id: "z", stream: "stderr" as const }
        const stdoutA = { ...log(7), event_id: "a" }
        const stdoutB = { ...log(7), event_id: "b" }
        const expected = [stderr, stdoutA, stdoutB]
        expect(mergeLiveLogs([stdoutB], [stdoutA, stderr])).toEqual(expected)
        expect(mergeLiveLogs([stderr, stdoutA], [stdoutB])).toEqual(expected)
    })

    it("retains the newest duplicate and does not mutate either source", () => {
        const old = { ...log(3), timestamp: "2026-07-20T00:00:00Z", message: "old" }
        const newest = { ...old, timestamp: "2026-07-20T00:00:01Z", message: "new" }
        const existing = [old, log(1)]
        const incoming = [newest, log(5), old]
        for (const entry of [...existing, ...incoming]) Object.freeze(entry)
        Object.freeze(existing)
        Object.freeze(incoming)
        expect(mergeLiveLogs(existing, incoming)).toEqual([log(5), newest, log(1)])
        expect(mergeLiveLogs([newest], [old])).toEqual([newest])
        expect(existing).toEqual([old, log(1)])
        expect(incoming).toEqual([newest, log(5), old])
    })

    it("prefers the highest sequence when duplicate event identities disagree", () => {
        const lower = { ...log(1), event_id: "same" }
        const higher = { ...log(8), event_id: "same" }
        expect(mergeLiveLogs([lower], [higher])).toEqual([higher])
        expect(mergeLiveLogs([higher], [lower])).toEqual([higher])
    })

    it("handles absent and empty pages and deduplicates page overlaps", () => {
        expect(logsFromPages()).toEqual([])
        expect(logsFromPages([])).toEqual([])
        expect(logsFromPages([
            { id: "job", workflow_id: "workflow", logs: [] },
            { id: "job", workflow_id: "workflow", logs: [log(2), log(1)] },
            { id: "job", workflow_id: "workflow", logs: [log(2)] },
        ])).toEqual([log(2), log(1)])
    })

    it("merges, deduplicates, and sorts live logs newest first", () => {
        expect(mergeLiveLogs([log(1), log(3)], [log(2), log(3)])).toEqual([
            log(3),
            log(2),
            log(1),
        ])
    })

    it("flattens retained pages using the same stable ordering", () => {
        expect(logsFromPages([
            { id: "job", workflow_id: "workflow", logs: [log(2)] },
            { id: "job", workflow_id: "workflow", logs: [log(1)] },
        ])).toEqual([log(2), log(1)])
    })

    it("normalizes downloaded log filenames", () => {
        expect(getDownloadFilename("run.json", "jsonl")).toBe("run.jsonl")
        expect(getDownloadFilename(" ", "txt")).toBe("logs.txt")
    })
})
