import { afterEach, expect, it, vi } from "vitest"

afterEach(() => {
    vi.unstubAllEnvs()
    vi.resetModules()
})

it.each(["production", "development", "test"] as const)("applies security headers to every route in %s", async (mode) => {
    vi.stubEnv("NODE_ENV", mode)
    vi.resetModules()
    const { default: config } = await import("./next.config")
    const rules = await config.headers!()
    expect(rules).toHaveLength(1)
    expect(rules[0].source).toBe("/:path*")
    const headers = Object.fromEntries(rules[0].headers.map(({ key, value }) => [key, value]))
    expect(headers["Strict-Transport-Security"]).toBe("max-age=63072000; includeSubDomains")
    expect(headers["X-Content-Type-Options"]).toBe("nosniff")
    expect(headers["X-Frame-Options"]).toBe("DENY")
    expect(headers["Referrer-Policy"]).toBe("strict-origin-when-cross-origin")
    const directives = headers["Content-Security-Policy"].split("; ")
    expect(directives).toContain("frame-ancestors 'none'")
    expect(directives).toContain("base-uri 'self'")
    expect(directives).toContain("form-action 'self'")
    expect(directives).toContain("connect-src 'self' http: https:")
    expect(directives.find((directive) => directive.startsWith("script-src "))).toBe(
        mode === "production" ? "script-src 'self' 'unsafe-inline'" : "script-src 'self' 'unsafe-inline' 'unsafe-eval'",
    )
    expect(config.output).toBe("standalone")
    expect(config.reactCompiler).toBe(true)
})
