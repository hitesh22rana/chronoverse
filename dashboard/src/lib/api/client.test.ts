import { describe, expect, it, vi } from "vitest"

import { createIdempotencyKey, fetchApi, fetchApiJson } from "./client"

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
        vi.stubEnv("NEXT_PUBLIC_API_URL", "https://api.example.com/edge")
        vi.resetModules()
        vi.stubGlobal("document", { cookie: "" })
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
            const { fetchApi: configuredFetchApi } = await import("./client")
            await configuredFetchApi("http://api.example.com/workflows", "boom", { method: "POST" })
            expect(calls[0].url).toBe("https://api.example.com/edge/auth/csrf")
            expect(calls.at(-1)?.headers.get("X-CSRF-Token")).toBe("fresh")
        } finally {
            vi.unstubAllGlobals()
            vi.unstubAllEnvs()
            vi.resetModules()
        }
    })

    it("refetches per mutation instead of reusing a stale token", async () => {
        vi.stubGlobal("document", { cookie: "" })
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

    // Single argument: also covers the default message and default options.
    it("uses the default message when the error body is blank", async () => {
        vi.stubGlobal("fetch", vi.fn(async () => new Response("   ", { status: 500 })))
        try {
            await expect(fetchApi("http://x/")).rejects.toThrow(new Error("API request failed"))
        } finally {
            vi.unstubAllGlobals()
        }
    })

    it("uses the given message when the error body cannot be read", async () => {
        vi.stubGlobal(
            "fetch",
            vi.fn(async () => ({ ok: false, text: () => Promise.reject(new Error("stream closed")) })),
        )
        try {
            await expect(fetchApi("http://x/", "boom")).rejects.toThrow(new Error("boom"))
        } finally {
            vi.unstubAllGlobals()
        }
    })

    it("keeps a caller-supplied Content-Type instead of defaulting it", async () => {
        const seen = new Headers()
        vi.stubGlobal(
            "fetch",
            vi.fn(async (_url: string, opts: RequestInit = {}) => {
                new Headers(opts.headers).forEach((v, k) => seen.set(k, v))
                return new Response("{}", { status: 200 })
            }),
        )
        try {
            await fetchApi("http://x/", "boom", { headers: { "Content-Type": "text/plain" } })
            expect(seen.get("Content-Type")).toBe("text/plain")
        } finally {
            vi.unstubAllGlobals()
        }
    })

    it("sends HEAD without fetching a csrf token", async () => {
        vi.stubGlobal("document", { cookie: "" })
        const urls: string[] = []
        vi.stubGlobal(
            "fetch",
            vi.fn(async (url: string) => {
                urls.push(url)
                return new Response(null, { status: 200 })
            }),
        )
        try {
            await fetchApi("http://x/", "boom", { method: "HEAD" })
            expect(urls).toEqual(["http://x/"])
        } finally {
            vi.unstubAllGlobals()
        }
    })

    it("still sends the request when there is no document to read a cookie from", async () => {
        vi.stubGlobal("document", undefined)
        const urls: string[] = []
        vi.stubGlobal(
            "fetch",
            vi.fn(async (url: string) => {
                urls.push(url)
                return new Response("{}", { status: 200 })
            }),
        )
        try {
            await fetchApi("http://x/", "boom", { method: "POST" })
            expect(urls).toEqual(["http://x/"])
        } finally {
            vi.unstubAllGlobals()
        }
    })

    // The non-OK body carries a token on purpose: the client must not take one
    // from a failed response, and a body that parses would mask that.
    it.each([
        ["is unreachable", () => Promise.reject(new TypeError("network down"))],
        ["answers non-OK", () => Promise.resolve(new Response(JSON.stringify({ csrfToken: "must-not-be-used" }), { status: 500 }))],
        ["answers without a token", () => Promise.resolve(new Response("{}", { status: 200 }))],
    ])("sends the request untokenized when the csrf endpoint %s", async (_case, csrfResponse) => {
        vi.stubGlobal("document", { cookie: "" })
        let token: string | null | undefined
        vi.stubGlobal(
            "fetch",
            vi.fn(async (url: string, opts: RequestInit = {}) => {
                if (url.endsWith("/auth/csrf")) {
                    return await csrfResponse()
                }
                token = new Headers(opts.headers).get("X-CSRF-Token")
                return new Response("{}", { status: 200 })
            }),
        )
        try {
            await fetchApi("http://x/", "boom", { method: "POST" })
            expect(token).toBeNull()
        } finally {
            vi.unstubAllGlobals()
        }
    })
})

describe("fetchApiJson", () => {
    // Single argument: also covers the default message and default options.
    it("returns the parsed body", async () => {
        vi.stubGlobal(
            "fetch",
            vi.fn(async () => new Response(JSON.stringify({ id: "w1", total: 2 }), { status: 200 })),
        )
        try {
            await expect(fetchApiJson("http://x/")).resolves.toEqual({ id: "w1", total: 2 })
        } finally {
            vi.unstubAllGlobals()
        }
    })

    // Delegates to fetchApi, so a failed request must reject with the same
    // message rather than surfacing a JSON parse failure.
    it("rejects with the fetchApi message instead of a parse error", async () => {
        vi.stubGlobal("fetch", vi.fn(async () => new Response("not json", { status: 500 })))
        try {
            await expect(fetchApiJson("http://x/", "boom")).rejects.toThrow(new Error("boom: not json"))
        } finally {
            vi.unstubAllGlobals()
        }
    })

    it("sends csrf with the body request like any other mutation", async () => {
        vi.stubGlobal("document", { cookie: "csrf=tok123" })
        let token: string | null = null
        vi.stubGlobal(
            "fetch",
            vi.fn(async (_url: string, opts: RequestInit = {}) => {
                token = new Headers(opts.headers).get("X-CSRF-Token")
                return new Response("{}", { status: 200 })
            }),
        )
        try {
            await fetchApiJson("http://x/", "boom", { method: "POST" })
            expect(token).toBe("tok123")
        } finally {
            vi.unstubAllGlobals()
        }
    })
})
