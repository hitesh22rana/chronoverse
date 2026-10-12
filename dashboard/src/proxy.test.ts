import { NextRequest } from "next/server"
import { describe, expect, it } from "vitest"

import { proxy } from "./proxy"

describe("authentication routing", () => {
    it.each([
        [undefined, undefined],
        ["session-token", undefined],
        [undefined, "csrf-token"],
        ["", "csrf-token"],
        ["session-token", ""],
    ])("requires both nonempty cookies for protected routes (%s, %s)", async (session, csrf) => {
        const request = new NextRequest("https://chronoverse.example/workflows?status=ACTIVE")
        if (session !== undefined) request.cookies.set("session", session)
        if (csrf !== undefined) request.cookies.set("csrf", csrf)

        const response = await proxy(request)

        expect(response.status).toBe(307)
        expect(response.headers.get("location")).toBe("https://chronoverse.example/login")
    })

    it.each(["/login", "/signup", "/login/reset", "/signup/verify"])("allows anonymous requests to %s", async (path) => {
        const response = await proxy(new NextRequest(`https://chronoverse.example${path}`))

        expect(response.headers.get("x-middleware-next")).toBe("1")
        expect(response.headers.get("location")).toBeNull()
    })

    it.each(["/login", "/signup", "/login/reset"])("redirects authenticated requests from %s", async (path) => {
        const request = new NextRequest(`https://chronoverse.example${path}?returnTo=/workflows`)
        request.cookies.set("session", "session-token")
        request.cookies.set("csrf", "csrf-token")

        const response = await proxy(request)

        expect(response.status).toBe(307)
        expect(response.headers.get("location")).toBe("https://chronoverse.example/")
    })

    it.each(["/", "/workflows", "/workflows/abc/jobs/123"])("passes authenticated requests to %s through", async (path) => {
        const request = new NextRequest(`https://chronoverse.example${path}`)
        request.cookies.set("session", "session-token")
        request.cookies.set("csrf", "csrf-token")

        const response = await proxy(request)

        expect(response.headers.get("x-middleware-next")).toBe("1")
        expect(response.headers.get("location")).toBeNull()
    })
})
