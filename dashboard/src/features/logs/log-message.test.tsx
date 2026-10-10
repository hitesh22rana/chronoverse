// @vitest-environment jsdom
import { cleanup, render } from "@testing-library/react"
import { afterEach, describe, expect, it } from "vitest"

import { parseLogMessage } from "./log-message"

const token = "0123456789abcdef0123456789abcdef"
const otherToken = "fedcba9876543210fedcba9876543210"
const start = `__CV_HL_START_${token}__`
const end = `__CV_HL_END_${token}__`
const highlight = (text: string) => `${start}${text}${end}`

function renderMessage(message: string, highlightToken: string | undefined = undefined, parseJson = false) {
    return render(<div>{parseLogMessage(message, highlightToken, parseJson)}</div>).container
}

function marks(container: HTMLElement) {
    return Array.from(container.querySelectorAll("mark"), (mark) => mark.textContent)
}

afterEach(cleanup)

describe("log message rendering", () => {
    it.each([undefined, "", "short", "G".repeat(32), token.toUpperCase()])(
        "preserves markers for an absent or invalid token %s",
        (highlightToken) => {
            const message = highlight("error")
            for (const parseJson of [false, true]) {
                const container = renderMessage(message, highlightToken, parseJson)
                expect(container.textContent).toBe(message)
                expect(marks(container)).toEqual([])
            }
        },
    )

    it.each([false, true])("renders multiple highlights in order with JSON mode %s", (parseJson) => {
        const container = renderMessage(`before ${highlight("error")} between ${highlight("error")} after`, token, parseJson)
        expect(container.textContent).toBe("before error between error after")
        expect(marks(container)).toEqual(["error", "error"])
    })

    it.each([false, true])("preserves plain, foreign and incomplete markers in JSON mode %s", (parseJson) => {
        for (const message of [
            "plain text",
            "",
            `${start}unfinished`,
            `unmatched ${end}`,
            `__CV_HL_START_${otherToken}__foreign__CV_HL_END_${otherToken}__`,
        ]) {
            const container = renderMessage(message, token, parseJson)
            expect(container.textContent).toBe(message)
            expect(marks(container)).toEqual([])
        }
    })

    it.each([false, true])("keeps an incomplete tail after a valid match in JSON mode %s", (parseJson) => {
        const container = renderMessage(`${highlight("first")} trailing ${start}unfinished`, token, parseJson)
        expect(container.textContent).toBe(`first trailing ${start}unfinished`)
        expect(marks(container)).toEqual(["first"])
    })

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

    it.each(['[1,{"ok":true}]', '"text"', "null", "42"])("formats JSON value %s without markers", (message) => {
        const container = renderMessage(message, undefined, true)
        expect(container.textContent).toBe(JSON.stringify(JSON.parse(message), null, 2))
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

    it.each([false, true])("escapes HTML inside highlighted and ordinary log text in JSON mode %s", (parseJson) => {
        const dangerous = '<img src=x onerror="alert(1)">'
        const container = renderMessage(`<script>alert(1)</script> ${highlight(dangerous)}`, token, parseJson)
        expect(container.textContent).toBe(`<script>alert(1)</script> ${dangerous}`)
        expect(marks(container)).toEqual([dangerous])
        expect(container.querySelector("script,img")).toBeNull()
    })
})
