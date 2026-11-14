import type { DownloadLogsFormat, JobLog, JobLogsResponse } from "@/features/logs/types"

export type JobLogWire = {
    event_id?: string
    timestamp?: string
    message?: string
    sequence_num?: number
    stream?: string
}

const compareLogsDesc = (a: JobLog, b: JobLog): number => {
    const sequenceOrder = b.sequence_num - a.sequence_num
    if (sequenceOrder !== 0) return sequenceOrder

    const streamOrder = a.stream.localeCompare(b.stream)
    if (streamOrder !== 0) return streamOrder

    const eventOrder = a.event_id.localeCompare(b.event_id)
    if (eventOrder !== 0) return eventOrder

    return b.timestamp.localeCompare(a.timestamp)
}

const sortLogsDesc = (logs: JobLog[]): JobLog[] => [...logs].sort(compareLogsDesc)

const uniqueLogsByKey = (logs: JobLog[]): JobLog[] => {
    const uniqueLogs = new Map<string, JobLog>()

    for (const log of logs) {
        const key = log.event_id
        const existingLog = uniqueLogs.get(key)
        if (!existingLog || compareLogsDesc(log, existingLog) < 0) {
            uniqueLogs.set(key, log)
        }
    }

    return Array.from(uniqueLogs.values())
}

export const normalizeJobLog = (log: JobLogWire, highlightToken?: string): JobLog => {
    if (typeof log.event_id !== "string" || log.event_id.length === 0) {
        throw new Error("Log event identity is required")
    }
    // Protobuf omits a zero uint32 sequence from JSON.
    const sequenceNum = log.sequence_num ?? 0
    if (!Number.isInteger(sequenceNum) || sequenceNum < 0 || sequenceNum > 0xffffffff) {
        throw new Error("Log sequence number must be a uint32")
    }

    return {
        event_id: log.event_id,
        highlightToken,
        timestamp: log.timestamp || "",
        message: log.message || "",
        sequence_num: sequenceNum,
        stream: log.stream === "stderr" ? "stderr" : "stdout",
    }
}

export const mergeLiveLogs = (existingLogs: JobLog[], newLogs: JobLog[]): JobLog[] =>
    sortLogsDesc(uniqueLogsByKey([...existingLogs, ...newLogs]))

// Callers pass pages normalized by the query layer (use-job-logs); raw wire pages may omit logs.
export const logsFromPages = (pages?: JobLogsResponse[]): JobLog[] =>
    pages?.length
        ? sortLogsDesc(uniqueLogsByKey(pages.flatMap((page) => page.logs)))
        : []

export const getDownloadFilename = (filename: string, format: DownloadLogsFormat) => {
    const base = (filename.trim() || "logs").replace(/\.(txt|json|jsonl)$/i, "")
    return `${base}.${format}`
}
