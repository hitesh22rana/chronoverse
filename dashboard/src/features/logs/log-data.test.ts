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
    it("normalizes wire field variants and stderr", () => {
        expect(normalizeJobLog({
            eventId: "job:7",
            sequenceNum: "7",
            message: "failed",
            stream: "stderr",
        })).toMatchObject({
            event_id: "job:7",
            sequence_num: 7,
            message: "failed",
            stream: "stderr",
        })
    })

    it.each([
        [{ event_id: "snake:8", eventId: "camel:9", sequence_num: "0", sequenceNum: 9 }, "snake:8", 0],
        [{ eventId: "job:segment:12" }, "job:segment:12", 12],
        [{ event_id: "", eventId: "job:4" }, "job:4", 4],
        [{ event_id: "not-a-sequence" }, "not-a-sequence", 0],
        [{ sequence_num: "invalid", sequenceNum: 4, event_id: "job:8" }, "job:8", 0],
        [{ sequenceNum: Infinity }, undefined, 0],
        [{ sequence_num: NaN }, undefined, 0],
        [{ sequence_num: -2 }, undefined, -2],
        [{}, undefined, 0],
    ])("normalizes compatible fields without overriding explicit sequences: %j", (wire, eventID, sequence) => {
        expect(normalizeJobLog(wire, "highlight")).toEqual({
            event_id: eventID,
            highlightToken: "highlight",
            sequence_num: sequence,
            timestamp: "",
            message: "",
            stream: "stdout",
        })
    })

    it("preserves timestamp, message, and defaults unknown streams to stdout", () => {
        expect(normalizeJobLog({
            sequence_num: "2.5", timestamp: "2026-07-20T00:00:00Z",
            message: "kept", stream: "unknown",
        })).toMatchObject({ sequence_num: 2.5, timestamp: "2026-07-20T00:00:00Z", message: "kept", stream: "stdout" })
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
        const existing = Object.freeze([old])
        const incoming = Object.freeze([newest, old])
        expect(mergeLiveLogs([...existing], [...incoming])).toEqual([newest])
        expect(mergeLiveLogs([newest], [old])).toEqual([newest])
        expect(existing).toEqual([old])
        expect(incoming).toEqual([newest, old])
    })

    it("deduplicates missing identities by the complete fallback key", () => {
        const first = { ...log(3), event_id: undefined, message: "a" }
        const second = { ...first, message: "b" }
        const otherTime = { ...first, timestamp: "2026-07-21T00:00:00Z" }
        expect(mergeLiveLogs([otherTime, second], [first, { ...first }])).toEqual([first, second, otherTime])
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
