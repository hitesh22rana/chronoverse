import { describe, expect, it } from "vitest"

import { safeActionUrl } from "./notification-data"

describe("safeActionUrl", () => {
    it("passes server-generated workflow paths through", () => {
        expect(safeActionUrl("/workflows/123e4567-e89b-12d3-a456-426614174000")).toBe(
            "/workflows/123e4567-e89b-12d3-a456-426614174000",
        )
        expect(safeActionUrl("/")).toBe("/")
        expect(safeActionUrl("/workflows/abc?tab=logs")).toBe("/workflows/abc?tab=logs")
    })

    it("falls back to / for absolute and scheme URLs", () => {
        expect(safeActionUrl("javascript:alert(1)")).toBe("/")
        expect(safeActionUrl("https://evil.example/phish")).toBe("/")
        expect(safeActionUrl("")).toBe("/")
    })

    it("falls back to / for protocol-relative URLs", () => {
        expect(safeActionUrl("//evil.example/path")).toBe("/")
    })

    it("falls back to / for backslash origin escapes", () => {
        expect(safeActionUrl("/\\evil.example/path")).toBe("/")
        expect(safeActionUrl("/\\/evil.example/path")).toBe("/")
    })

    it("passes mid-path backslashes through normalized: they stay same-origin", () => {
        expect(safeActionUrl("/workflows\\evil.example")).toBe("/workflows/evil.example")
    })

    it("normalizes synthetic-origin URLs to origin-relative paths", () => {
        expect(safeActionUrl("//localhost/path")).toBe("/path")
        expect(safeActionUrl("http://localhost/path")).toBe("/path")
        expect(safeActionUrl("/\t/localhost/path")).toBe("/path")
    })

    it("falls back to / when the normalized path itself is protocol-relative", () => {
        expect(safeActionUrl("http://localhost//evil")).toBe("/")
    })

    it("falls back to / for control characters stripped before parsing", () => {
        expect(safeActionUrl("/\t/evil.example")).toBe("/")
        expect(safeActionUrl("/\n/evil.example")).toBe("/")
        expect(safeActionUrl("/\r/evil.example")).toBe("/")
    })
})
