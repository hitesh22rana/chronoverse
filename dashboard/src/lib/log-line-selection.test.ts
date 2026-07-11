import { describe, expect, it } from "vitest"

import {
    buildLogViewerUrl,
    formatLogLineSelection,
    getSelectedLogText,
    isLogLineSelected,
    normalizeLogLineSelection,
    parseLogLineSelection,
    shouldFetchMoreLogsForSelection,
} from "./log-line-selection"

describe("log line fragments", () => {
    it("parses single lines and ranges", () => {
        expect(parseLogLineSelection("#L6")).toEqual({ start: 6, end: 6 })
        expect(parseLogLineSelection("#L6-L19")).toEqual({ start: 6, end: 19 })
    })

    it("normalizes reversed ranges", () => {
        expect(parseLogLineSelection("#L19-L6")).toEqual({ start: 6, end: 19 })
        expect(normalizeLogLineSelection(19, 6)).toEqual({ start: 6, end: 19 })
        expect(formatLogLineSelection({ start: 19, end: 6 })).toBe("#L6-L19")
    })

    it("rejects invalid fragments and unsafe line numbers", () => {
        for (const fragment of ["", "#", "#L0", "#L-1", "#l6", "#L6-19", "#L6-L0", "#L1.5"]) {
            expect(parseLogLineSelection(fragment)).toBeNull()
        }

        expect(parseLogLineSelection(`#L${Number.MAX_SAFE_INTEGER + 1}`)).toBeNull()
    })

    it("formats canonical fragments", () => {
        expect(formatLogLineSelection({ start: 6, end: 6 })).toBe("#L6")
        expect(formatLogLineSelection({ start: 6, end: 19 })).toBe("#L6-L19")
    })

    it("builds a pathname-based URL when filters are cleared", () => {
        expect(buildLogViewerUrl("/jobs/1", "", "#L1-L7")).toBe("/jobs/1#L1-L7")
        expect(buildLogViewerUrl("/jobs/1", "q=level", "#L1-L7")).toBe("/jobs/1?q=level#L1-L7")
        expect(buildLogViewerUrl("/jobs/1", "q=level", "")).toBe("/jobs/1?q=level")
    })
})

describe("log line selection", () => {
    it("checks whether a line is within the selected range", () => {
        const selection = { start: 6, end: 19 }

        expect(isLogLineSelected(5, selection)).toBe(false)
        expect(isLogLineSelected(6, selection)).toBe(true)
        expect(isLogLineSelected(19, selection)).toBe(true)
        expect(isLogLineSelected(20, selection)).toBe(false)
    })

    it("requests cursor pages until the maximum selected line is loaded", () => {
        const selection = { start: 6, end: 119 }

        expect(shouldFetchMoreLogsForSelection(selection, 100, true)).toBe(true)
        expect(shouldFetchMoreLogsForSelection(selection, 119, true)).toBe(false)
        expect(shouldFetchMoreLogsForSelection(selection, 100, false)).toBe(false)
        expect(shouldFetchMoreLogsForSelection(null, 0, true)).toBe(false)
    })

    it("copies selected log messages in displayed order", () => {
        const messages = ["newest", "middle", "oldest"]

        expect(getSelectedLogText(messages, { start: 2, end: 2 })).toBe("middle")
        expect(getSelectedLogText(messages, { start: 1, end: 3 })).toBe("newest\nmiddle\noldest")
    })
})
