// @vitest-environment jsdom
import { cleanup, render } from "@testing-library/react"
import { afterEach, describe, expect, it } from "vitest"

import { parseLogMessage } from "./log-message"

const token = "0123456789abcdef0123456789abcdef"
const otherToken = "fedcba9876543210fedcba9876543210"
const shortHexToken = "0123456789abcdef"
const marked = (markerToken: string, text: string) => `__CV_HL_START_${markerToken}__${text}__CV_HL_END_${markerToken}__`
const start = `__CV_HL_START_${token}__`
const end = `__CV_HL_END_${token}__`
const highlight = (text: string) => marked(token, text)
const jsonModes = [["raw", false], ["JSON", true]] as const

function renderMessage(message: string, highlightToken: string | undefined = undefined, parseJson = false) {
    return render(<div>{parseLogMessage(message, highlightToken, parseJson)}</div>).container
}

function marks(container: HTMLElement) {
    return Array.from(container.querySelectorAll("mark"), (mark) => mark.textContent)
}

afterEach(cleanup)

describe("log message rendering", () => {
    // Matching invalid-token markers make these assertions depend on rejection.
    it.each([undefined, "", "short", shortHexToken, "G".repeat(32), token.toUpperCase()])(
        "preserves markers for an absent or invalid token %s",
        (highlightToken) => {
            const message = marked(highlightToken ?? "undefined", "error")
            for (const parseJson of [false, true]) {
                const container = renderMessage(message, highlightToken, parseJson)
                expect(container.textContent).toBe(message)
                expect(marks(container)).toEqual([])
            }
        },
    )

    it.each(jsonModes)("renders multiple highlights in order in %s mode", (_label, parseJson) => {
        const container = renderMessage(`before ${highlight("error")} between ${highlight("error")} after`, token, parseJson)
        expect(container.textContent).toBe("before error between error after")
        expect(marks(container)).toEqual(["error", "error"])
    })

    it.each(jsonModes)("preserves plain, foreign and incomplete markers in %s mode", (_label, parseJson) => {
        for (const message of [
            "plain text",
            "",
            `${start}unfinished`,
            `unmatched ${end}`,
            marked(otherToken, "foreign"),
        ]) {
            const container = renderMessage(message, token, parseJson)
            expect(container.textContent).toBe(message)
            expect(marks(container)).toEqual([])
        }
    })

    // Incomplete markers remain literal text.
    it.each(jsonModes)("keeps an incomplete tail after a valid match in %s mode", (_label, parseJson) => {
        const container = renderMessage(`${highlight("first")} trailing ${start}unfinished`, token, parseJson)
        expect(container.textContent).toBe(`first trailing ${start}unfinished`)
        expect(marks(container)).toEqual(["first"])
    })

    // Raw rendering keeps empty marks; JSON rendering skips empty segments.
    it("renders empty segments without losing adjacent text", () => {
        const message = `before${highlight("")}after`
        const raw = renderMessage(message, token)
        expect(raw.textContent).toBe("beforeafter")
        expect(marks(raw)).toEqual([""])
        const parsed = renderMessage(message, token, true)
        expect(parsed.textContent).toBe("beforeafter")
        expect(marks(parsed)).toEqual([])
    })

    it("formats a complete JSON object and keeps string highlights", () => {
        const container = renderMessage(`{"level":"${highlight("error")}","count":2}`, token, true)
        expect(container.textContent).toBe(JSON.stringify({ level: "error", count: 2 }, null, 2))
        expect(marks(container)).toEqual(["error"])
    })

    it("highlights the selected occurrence instead of an earlier equal JSON value", () => {
        const container = renderMessage(`{"first":"error","second":"${highlight("error")}"}`, token, true)
        expect(container.textContent).toBe(JSON.stringify({ first: "error", second: "error" }, null, 2))
        expect(marks(container)).toEqual(["error"])
        expect(container.querySelector("mark")?.previousSibling?.textContent).toBe('{\n  "first": "error",\n  "second": "')
    })

    it("highlights repeated matches inside a single JSON string", () => {
        const container = renderMessage(`{"a":"${highlight("x")}${highlight("x")}"}`, token, true)
        expect(container.textContent).toBe(JSON.stringify({ a: "xx" }, null, 2))
        expect(marks(container)).toEqual(["x", "x"])
    })

    it("preserves the selected escaped substring and literal placeholder-like text", () => {
        const container = renderMessage(`{"first":"a\\nb","second":"a${highlight("\\n")}b","literal":"__CV_PLACEHOLDER_0__"}`, token, true)
        expect(container.textContent).toBe('{\n  "first": "a\\nb",\n  "second": "a\\nb",\n  "literal": "__CV_PLACEHOLDER_0__"\n}')
        expect(marks(container)).toEqual(["\\n"])
        expect(container.querySelector("mark")?.previousSibling?.textContent).toBe('{\n  "first": "a\\nb",\n  "second": "a')
    })

    it("retains numeric spelling, escaped Unicode and source key order", () => {
        const container = renderMessage(`{"10":1e2,"2":${highlight("1e2")},"text":"${highlight("\\u0061")}"}`, token, true)
        expect(container.textContent).toBe('{\n  "10": 1e2,\n  "2": 1e2,\n  "text": "\\u0061"\n}')
        expect(marks(container)).toEqual(["1e2", "\\u0061"])
    })

    it("keeps structural selections across inserted indentation", () => {
        const container = renderMessage(`{"empty":[],"selected":${highlight('[true,{},null,[2]]')}}`, token, true)
        expect(container.textContent).toBe(JSON.stringify({ empty: [], selected: [true, {}, null, [2]] }, null, 2))
        expect(marks(container)).toEqual(['[\n    true,\n    {},\n    null,\n    [\n      2\n    ]\n  ]'])
    })

    it("does not move an embedded JSON match into equal preceding plain text", () => {
        const container = renderMessage(`error {"first":"error","selected":"${highlight("error")}"}`, token, true)
        expect(container.querySelector("mark")?.previousSibling?.textContent).toBe('error {\n  "first": "error",\n  "selected": "')
        expect(marks(container)).toEqual(["error"])
    })

    it("formats and highlights embedded JSON beyond the spread argument limit", () => {
        // Exceed spread argument limits while preserving formatting and highlighting.
        const values = Array.from({ length: 50000 }, (_, index) => index)
        const selected = String(values.length)
        const container = renderMessage(`error {"values":[${values.join(",")}],"selected":${highlight(selected)}}`, token, true)
        expect(container.textContent).toContain('error {\n  "values": [\n    0,\n    1,\n    2,\n')
        expect(container.textContent?.endsWith(`"selected": ${selected}\n}`)).toBe(true)
        expect(marks(container)).toEqual([selected])
        expect(container.querySelector("mark")?.previousSibling?.textContent?.endsWith('"selected": ')).toBe(true)
    })

    it("falls back instead of overflowing the string limit on deeply nested JSON", () => {
        // 20k nesting fits the 256 KiB line cap but would indent to ~800 MB, past the string limit.
        const arrays = `${"[".repeat(20000)}0${"]".repeat(20000)}`
        const arrayContainer = renderMessage(arrays, token, true)
        expect(arrayContainer.textContent).toBe(arrays)
        expect(marks(arrayContainer)).toEqual([])

        // Nested objects keep their text: the fallback regex only indents its one shallow match.
        const objects = `${'{"a":'.repeat(20000)}1${"}".repeat(20000)}`
        const objectContainer = renderMessage(objects, token, true)
        expect(objectContainer.textContent?.startsWith('{"a":{"a":{"a":')).toBe(true)
        expect(objectContainer.textContent?.length).toBeLessThan(objects.length * 2)
        expect(marks(objectContainer)).toEqual([])
    })

    it.each([
        ['[1,{"ok":true}]', JSON.stringify([1, { ok: true }], null, 2)],
        ['"text"', JSON.stringify("text")],
        ["null", "null"],
        ["42", "42"],
    ])("formats JSON value %s without markers", (message, expected) => {
        const container = renderMessage(message, undefined, true)
        expect(container.textContent).toBe(expected)
        expect(marks(container)).toEqual([])
    })

    it("formats embedded JSON objects while retaining the surrounding log text", () => {
        const container = renderMessage(`prefix {"level":"${highlight("warn")}"} middle {"ok":true} suffix`, token, true)
        expect(container.textContent).toBe(`prefix ${JSON.stringify({ level: "warn" }, null, 2)} middle ${JSON.stringify({ ok: true }, null, 2)} suffix`)
        expect(marks(container)).toEqual(["warn"])
    })

    it("preserves malformed embedded JSON and continues formatting valid objects", () => {
        const container = renderMessage(`prefix {broken: ${highlight("oops")}} then {"ok":true}`, token, true)
        expect(container.textContent).toBe(`prefix {broken: oops} then ${JSON.stringify({ ok: true }, null, 2)}`)
        expect(marks(container)).toEqual(["oops"])
    })

    it("skips highlights removed by JSON formatting and still renders later matches", () => {
        const container = renderMessage(`${highlight("   ")}{"level":"${highlight("error")}"}`, token, true)
        expect(container.textContent).toBe(JSON.stringify({ level: "error" }, null, 2))
        expect(marks(container)).toEqual(["error"])
    })

    it.each(jsonModes)("escapes HTML inside highlighted and ordinary log text in %s mode", (_label, parseJson) => {
        const dangerous = '<img src=x onerror="alert(1)">'
        const container = renderMessage(`<script>alert(1)</script> ${highlight(dangerous)}`, token, parseJson)
        expect(container.textContent).toBe(`<script>alert(1)</script> ${dangerous}`)
        expect(marks(container)).toEqual([dangerous])
        expect(container.querySelector("script,img")).toBeNull()
    })
})
