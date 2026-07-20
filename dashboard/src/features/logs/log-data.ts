import type { DownloadLogsFormat, JobLog, JobLogsResponse } from "@/features/logs/types"

export type JobLogWire = {
    event_id?: string
    eventId?: string
    timestamp?: string
    message?: string
    sequence_num?: number | string
    sequenceNum?: number | string
    stream?: string
}

const logKey = (log: JobLog): string =>
    log.event_id ?? `${log.sequence_num}:${log.stream}:${log.timestamp}:${log.message}`

const sequenceNumFromEventID = (eventID?: string): number | undefined => {
    if (!eventID) return undefined

    const sequenceNum = Number(eventID.split(":").at(-1))
    return Number.isFinite(sequenceNum) ? sequenceNum : undefined
}

const compareLogsDesc = (a: JobLog, b: JobLog): number => {
    const sequenceOrder = b.sequence_num - a.sequence_num
    if (sequenceOrder !== 0) return sequenceOrder

    const streamOrder = a.stream.localeCompare(b.stream)
    if (streamOrder !== 0) return streamOrder

    const eventOrder = (a.event_id || logKey(a)).localeCompare(b.event_id || logKey(b))
    if (eventOrder !== 0) return eventOrder

    return b.timestamp.localeCompare(a.timestamp)
}

const sortLogsDesc = (logs: JobLog[]): JobLog[] => [...logs].sort(compareLogsDesc)

const uniqueLogsByKey = (logs: JobLog[]): JobLog[] => {
    const uniqueLogs = new Map<string, JobLog>()

    for (const log of logs) {
        const key = logKey(log)
        const existingLog = uniqueLogs.get(key)
        if (!existingLog || compareLogsDesc(log, existingLog) < 0) {
            uniqueLogs.set(key, log)
        }
    }

    return Array.from(uniqueLogs.values())
}

export const normalizeJobLog = (log: JobLogWire, highlightToken?: string): JobLog => {
    const eventID = log.event_id || log.eventId
    const sequenceNum = Number(
        log.sequence_num ?? log.sequenceNum ?? sequenceNumFromEventID(eventID) ?? 0,
    )

    return {
        event_id: eventID,
        highlightToken,
        timestamp: log.timestamp || "",
        message: log.message || "",
        sequence_num: Number.isFinite(sequenceNum) ? sequenceNum : 0,
        stream: log.stream === "stderr" ? "stderr" : "stdout",
    }
}

export const mergeLiveLogs = (existingLogs: JobLog[], newLogs: JobLog[]): JobLog[] =>
    sortLogsDesc(uniqueLogsByKey([...existingLogs, ...newLogs]))

export const logsFromPages = (pages?: JobLogsResponse[]): JobLog[] =>
    pages?.length
        ? sortLogsDesc(uniqueLogsByKey(pages.flatMap((page) => page?.logs || [])))
        : []

export const getDownloadFilename = (filename: string, format: DownloadLogsFormat) => {
    const base = (filename.trim() || "logs").replace(/\.(txt|json|jsonl)$/i, "")
    return `${base}.${format}`
}
