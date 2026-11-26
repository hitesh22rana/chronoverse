import { describe, expect, it } from "vitest"
import { readLogDownload } from "./log-download"
import type { DownloadLogsFormat } from "./types"

const record = { timestamp: "2026-11-26T00:00:00Z", sequence_num: 1, stream: "stdout", event_id: "event-1" }
const raw = '{\n "name":"🛰", "id":9007199254740993\n}'
const line = JSON.stringify({ type: "log", ...record, message_raw: raw }) + "\n"
const complete = '{"type":"complete"}\n'
const metadata = { id: "job", workflow_id: "workflow", filters: { q: "name", stream: "stdout" } }
const headers = { "Content-Type": "application/x-ndjson" }
const response = (body: string) => new Response(body, { headers })
const download = (body: Response, format: DownloadLogsFormat = "jsonl", limit = 4096) =>
    readLogDownload(body, { format, ...metadata }, limit)

describe("retained log export", () => {
    it.each(["txt", "json", "jsonl"] as const)("assembles %s across split UTF-8 and NDJSON records", async (format) => {
        const bytes = new TextEncoder().encode(line + complete)
        // Deliberately split every code point and record across network reads.
        let index = 0
        const stream = new ReadableStream<Uint8Array>({
            pull(controller) {
                if (index === bytes.length) controller.close()
                else controller.enqueue(bytes.subarray(index, ++index))
            },
        })
        const body = await (await download(new Response(stream, { headers }), format)).text()
        if (format === "txt") {
            expect(body).toBe(raw + "\n")
        } else {
            expect(body).toContain('"id":9007199254740993')
            expect(body).not.toContain('"type"')
            if (format === "jsonl") {
                expect(body.trimEnd().split("\n")).toHaveLength(1)
                expect(JSON.parse(body)).toMatchObject({ ...record, message_raw: raw })
            } else {
                expect(JSON.parse(body)).toMatchObject({ ...metadata, logs: [{ ...record, message_raw: raw }] })
            }
        }
    })

    it("saves an empty completed export", async () => {
        expect(JSON.parse(await (await download(response(complete), "json")).text())).toEqual({ ...metadata, logs: [] })
    })

    it.each([
        ["missing completion", line],
        ["unfinished completion line", line + complete.trimEnd()],
        ["duplicate completion", complete + complete],
        ["completion with extra fields", '{"type":"complete","message":"unexpected"}\n'],
        ["unknown log stream", line.replace("stdout", "other") + complete],
        ["data after completion", complete + line],
        ["trailing whitespace", complete + " "],
        ["unknown record", '{"type":"unknown"}\n' + complete],
        ["incomplete fields", '{"type":"log","message_raw":"x"}\n' + complete],
        ["malformed JSON", '{"type":"log"\n' + complete],
        ["backend failure", line + '{"type":"error","message":"stream failed"}\n'],
    ])("rejects %s", async (_, wire) => {
        await expect(download(response(wire))).rejects.toThrow()
    })

    it("rejects a transport failure after a completion record", async () => {
        let sent = false
        const transportError = new DOMException("Disconnected", "AbortError")
        const stream = new ReadableStream({
            pull(controller) {
                if (sent) controller.error(transportError)
                else controller.enqueue(new TextEncoder().encode(complete))
                sent = true
            },
        })
        await expect(download(new Response(stream, { headers }))).rejects.toThrow("Disconnected")
    })

    it("bounds wire bytes and cancels the response", async () => {
        let canceled = false
        let sent = false
        const stream = new ReadableStream<Uint8Array>({
            pull(controller) {
                if (!sent) controller.enqueue(new TextEncoder().encode(line.repeat(50)))
                sent = true
            },
            cancel() { canceled = true },
        })
        await expect(download(new Response(stream, { headers }), "txt", 100)).rejects.toThrow("limit")
        expect(canceled).toBe(true)
    })

    it("bounds output bytes when JSON embeds the raw message again", async () => {
        const wire = line + complete
        await expect(download(response(wire), "json", new TextEncoder().encode(wire).byteLength)).rejects.toThrow("limit")
    })

    it("rejects a legacy response without an NDJSON body", async () => {
        await expect(download(new Response("legacy text"))).rejects.toThrow("Invalid log download response")
    })
})
