// @vitest-environment jsdom
import { cleanup, render } from "@testing-library/react"
import { afterEach, describe, expect, it } from "vitest"

import { parseLogMessage } from "./log-message"

const token = "0123456789abcdef0123456789abcdef"
const otherToken = "fedcba9876543210fedcba9876543210"
/** Well-formed hex, but not the 32 characters the backend sends. */
const shortHexToken = "0123456789abcdef"
/** The markers the search service emits around a match, carrying a given token. */
const marked = (markerToken: string, text: string) => `__CV_HL_START_${markerToken}__${text}__CV_HL_END_${markerToken}__`
const start = `__CV_HL_START_${token}__`
const end = `__CV_HL_END_${token}__`
const highlight = (text: string) => marked(token, text)
/** The `parseJson` argument under a label a test title can read. */
const jsonModes = [["raw", false], ["JSON", true]] as const

function renderMessage(message: string, highlightToken: string | undefined = undefined, parseJson = false) {
    return render(<div>{parseLogMessage(message, highlightToken, parseJson)}</div>).container
}

function marks(container: HTMLElement) {
    return Array.from(container.querySelectorAll("mark"), (mark) => mark.textContent)
}

afterEach(cleanup)

describe("log message rendering", () => {
    /**
     * Each message carries markers built from the token under test, so the
     * expectation only holds while that token is still rejected: the tags the
     * code derives are the ones in the message, and a rejected token is what
     * leaves them unrecognised. A message built from the valid token instead
     * would give the code nothing to find and pass with or without the check.
     * The rejected tokens span what the check rules out: nothing, too short,
     * the wrong length for well-formed hex, a non-hex character, and the
     * uppercase hex a case-insensitive encoder would send. For an absent token
     * the candidate is the literal `undefined` that interpolating a missing
     * value would produce, so an absent token is caught when its absence leaks
     * into the tags instead of stopping them.
     */
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

    // An unmatched start marker has no closing tag, so it reaches the reader
    // verbatim: there is no match to mark and nothing to drop it from.
    it.each(jsonModes)("keeps an incomplete tail after a valid match in %s mode", (_label, parseJson) => {
        const container = renderMessage(`${highlight("first")} trailing ${start}unfinished`, token, parseJson)
        expect(container.textContent).toBe(`first trailing ${start}unfinished`)
        expect(marks(container)).toEqual(["first"])
    })

    // The two paths disagree about an empty segment on purpose: the raw path
    // marks what the search service bracketed, the parsed path has no bracket
    // left to find once formatting has rewritten the text.
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

    // The expectation is written out rather than re-parsed from the input, so a
    // change to the indent or to what a scalar serialises as fails here.
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
