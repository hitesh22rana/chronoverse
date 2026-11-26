import type { DownloadLogsFormat } from "./types"

type LogDownload = {
    format: DownloadLogsFormat
    id: string
    workflow_id: string
    filters: { q: string; stream: string }
}

type WireLog = {
    timestamp: string
    sequence_num: number
    stream: string
    event_id: string
    message_raw: string
}

const mimeTypes = { txt: "text/plain", json: "application/json", jsonl: "application/x-ndjson" }

function formatLog(record: WireLog, format: DownloadLogsFormat): string {
    if (format === "txt") return record.message_raw + "\n"

    const { timestamp, sequence_num, stream, event_id, message_raw } = record
    const encoded = JSON.stringify({ timestamp, sequence_num, stream, event_id, message_raw })
    try {
        JSON.parse(message_raw)
    } catch {
        return encoded
    }
    // Retain the original number tokens and keep JSONL records on one physical line.
    return encoded.slice(0, -1) + ',"message_json":' + message_raw.replace(/[\r\n]/g, " ") + "}"
}

export async function readLogDownload(response: Response, options: LogDownload, byteLimit: number): Promise<Blob> {
    if (response.headers.get("content-type")?.split(";")[0].trim() !== "application/x-ndjson" || !response.body) {
        throw new Error("Invalid log download response")
    }

    const reader = response.body.getReader()
    const decoder = new TextDecoder("utf-8", { fatal: true })
    const encoder = new TextEncoder()
    const chunks: BlobPart[] = []
    const { format, ...metadata } = options
    const pending: string[] = []
    let output = format === "json" ? JSON.stringify(metadata).slice(0, -1) + ',"logs":[' : ""
    let wireBytes = 0
    let outputBytes = 0
    let hasLogs = false
    let complete = false
    const limitError = () => new Error(`Log download exceeds the ${byteLimit / (1024 * 1024)} MiB limit. Narrow the filters and retry.`)

    const bufferOutput = () => {
        if (!output) return
        const bytes = encoder.encode(output)
        outputBytes += bytes.byteLength
        if (outputBytes > byteLimit) throw limitError()
        chunks.push(bytes)
        output = ""
    }

    try {
        for (;;) {
            const next = await reader.read()
            if (next.done) break
            wireBytes += next.value.byteLength
            if (wireBytes > byteLimit) throw limitError()

            const text = decoder.decode(next.value, { stream: true })
            let start = 0
            for (let end = text.indexOf("\n"); end !== -1; end = text.indexOf("\n", start)) {
                if (complete) throw new Error("Unexpected data after log download completion")
                let line = text.slice(start, end)
                if (pending.length) {
                    pending.push(line)
                    line = pending.join("")
                    pending.length = 0
                }
                const record = JSON.parse(line) as (Partial<WireLog> & { type?: string }) | null
                start = end + 1
                if (record?.type === "complete") {
                    if (Object.keys(record).length !== 1) throw new Error("Invalid log download completion")
                    complete = true
                    continue
                }
                if (record?.type === "error") throw new Error("Log download failed before completion")
                if (
                    record?.type !== "log" || typeof record.message_raw !== "string" ||
                    typeof record.timestamp !== "string" || typeof record.event_id !== "string" ||
                    (record.stream !== "stdout" && record.stream !== "stderr") || typeof record.sequence_num !== "number" ||
                    !Number.isInteger(record.sequence_num) || record.sequence_num < 0 || record.sequence_num > 0xffffffff
                ) {
                    throw new Error("Invalid log download record")
                }
                if (format === "json" && hasLogs) output += ","
                output += formatLog(record as WireLog, format)
                if (format === "jsonl") output += "\n"
                hasLogs = true
            }
            if (start < text.length) pending.push(text.slice(start))
            bufferOutput()
        }

        const trailing = decoder.decode()
        if (pending.length || trailing || !complete) throw new Error("Log download ended before completion")
        if (format === "json") output = "]}"
        bufferOutput()
        return new Blob(chunks, { type: mimeTypes[format] })
    } catch (error) {
        await reader.cancel().catch(() => {})
        throw error
    } finally {
        reader.releaseLock()
    }
}
