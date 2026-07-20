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
