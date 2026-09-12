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
            await fetchApi("http://x/", "boom", { method: "POST" })
            expect(seen.get("X-CSRF-Token")).toBe("tok123")
        } finally {
            vi.unstubAllGlobals()
        }
    })

    it("fetches the token over CORS when the cookie is unreadable", async () => {
        vi.stubGlobal("document", { cookie: "" })
        vi.stubGlobal("window", { location: { origin: "http://app.example.com" } })
        const calls: Array<{ url: string; headers: Headers }> = []
        vi.stubGlobal(
            "fetch",
            vi.fn(async (url: string, opts: RequestInit = {}) => {
                calls.push({ url, headers: new Headers(opts.headers) })
                if (url.endsWith("/auth/csrf")) {
                    return new Response(JSON.stringify({ csrfToken: "fresh" }), { status: 200 })
                }
                return new Response("{}", { status: 200 })
            }),
        )
        try {
            await fetchApi("http://api.example.com/workflows", "boom", { method: "POST" })
            expect(calls[0].url).toBe("http://api.example.com/auth/csrf")
            expect(calls.at(-1)?.headers.get("X-CSRF-Token")).toBe("fresh")
        } finally {
            vi.unstubAllGlobals()
        }
    })

    it("refetches per mutation instead of reusing a stale token", async () => {
        vi.stubGlobal("document", { cookie: "" })
        vi.stubGlobal("window", { location: { origin: "http://app.example.com" } })
        const csrfCalls: string[] = []
        const mutationTokens: Array<string | null> = []
        vi.stubGlobal(
            "fetch",
            vi.fn(async (url: string, opts: RequestInit = {}) => {
                if (url.endsWith("/auth/csrf")) {
                    csrfCalls.push(url)
                    return new Response(JSON.stringify({ csrfToken: `tok${csrfCalls.length}` }), {
                        status: 200,
                    })
                }
                mutationTokens.push(new Headers(opts.headers).get("X-CSRF-Token"))
                return new Response("{}", { status: 200 })
            }),
        )
        try {
            await fetchApi("http://api.example.com/workflows", "boom", { method: "POST" })
            await fetchApi("http://api.example.com/workflows", "boom", { method: "POST" })
            expect(csrfCalls).toHaveLength(2)
            expect(mutationTokens).toEqual(["tok1", "tok2"])
        } finally {
            vi.unstubAllGlobals()
        }
    })

    it("resolves relative API URLs against the browser origin", async () => {
        vi.stubGlobal("document", { cookie: "" })
        vi.stubGlobal("window", { location: { origin: "http://app.example.com" } })
        const calls: Array<{ url: string; headers: Headers }> = []
        vi.stubGlobal(
            "fetch",
            vi.fn(async (url: string, opts: RequestInit = {}) => {
                calls.push({ url, headers: new Headers(opts.headers) })
                if (url.endsWith("/auth/csrf")) {
                    return new Response(JSON.stringify({ csrfToken: "same-origin" }), { status: 200 })
                }
                return new Response("{}", { status: 200 })
            }),
        )
        try {
            await fetchApi("/workflows", "boom", { method: "POST" })
            expect(calls[0].url).toBe("http://app.example.com/auth/csrf")
            expect(calls.at(-1)?.headers.get("X-CSRF-Token")).toBe("same-origin")
        } finally {
            vi.unstubAllGlobals()
        }
    })
})
