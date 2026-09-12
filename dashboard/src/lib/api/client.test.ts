import { describe, expect, it, vi } from "vitest"

import { createIdempotencyKey, fetchApi } from "./client"

describe("createIdempotencyKey", () => {
    it("creates cryptographically secure UUID command identities", () => {
        expect(createIdempotencyKey()).toMatch(
            /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/,
        )
    })
})

describe("fetchApi", () => {
    it("echoes the csrf cookie as X-CSRF-Token", async () => {
        vi.stubGlobal("document", { cookie: "csrf=tok123" })
        const seen = new Headers()
        vi.stubGlobal(
            "fetch",
            vi.fn(async (_url: string, opts: RequestInit = {}) => {
                new Headers(opts.headers).forEach((v, k) => seen.set(k, v))
                return new Response("{}", { status: 200 })
            }),
        )
        try {
            await fetchApi("http://x/", "boom")
            expect(seen.get("X-CSRF-Token")).toBe("tok123")
        } finally {
            vi.unstubAllGlobals()
        }
    })
})
