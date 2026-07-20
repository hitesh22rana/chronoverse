import { describe, expect, it } from "vitest"

import { createApiEndpoints, withQuery } from "./endpoints"

describe("createApiEndpoints", () => {
    it("normalizes the base URL and builds static routes", () => {
        const endpoints = createApiEndpoints("https://api.example.com/")

        expect(endpoints.auth.login).toBe("https://api.example.com/auth/login")
        expect(endpoints.users).toBe("https://api.example.com/users")
        expect(endpoints.analytics.user).toBe("https://api.example.com/analytics")
    })

    it("encodes dynamic workflow and job path segments", () => {
        const endpoints = createApiEndpoints("https://api.example.com")

        expect(endpoints.workflows.jobs.detail("workflow/one", "job two")).toBe(
            "https://api.example.com/workflows/workflow%2Fone/jobs/job%20two",
        )
        expect(endpoints.workflows.jobs.rawLogs("workflow/one", "job two")).toBe(
            "https://api.example.com/workflows/workflow%2Fone/jobs/job%20two/logs/raw",
        )
    })

    it("supports same-origin API routes when no base URL is configured", () => {
        expect(createApiEndpoints().workflows.list).toBe("/workflows")
    })
})

describe("withQuery", () => {
    it("adds non-empty query parameters", () => {
        expect(withQuery("/workflows", new URLSearchParams({ cursor: "next page" }))).toBe(
            "/workflows?cursor=next+page",
        )
    })

    it("leaves URLs unchanged for empty parameters", () => {
        expect(withQuery("/workflows", "")).toBe("/workflows")
    })
})
