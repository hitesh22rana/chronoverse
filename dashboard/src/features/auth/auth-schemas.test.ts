import { describe, expect, it } from "vitest"

import { loginSchema, signupSchema } from "./auth-schemas"

describe("loginSchema", () => {
    it("accepts valid credentials", () => {
        expect(loginSchema.safeParse({
            email: "user@example.com",
            password: "password",
        }).success).toBe(true)
    })

    it.each([
        [{ email: "not-an-email", password: "password" }, "email"],
        [{ email: "user@example.com", password: "" }, "password"],
    ])("rejects invalid credentials %#", (credentials, field) => {
        const result = loginSchema.safeParse(credentials)

        expect(result.success).toBe(false)
        if (!result.success) {
            expect(result.error.issues[0]?.path).toEqual([field])
        }
    })

    it.each([
        [{ email: "user@example.com", password: "short" }],
        [{ email: "user@example.com", password: "a".repeat(73) }],
        // 4 emoji are 8 UTF-16 units but 4 runes; the server counts runes.
        [{ email: "user@example.com", password: "😀".repeat(4) }],
    ])("rejects login passwords outside 8-72 runes %#", (credentials) => {
        const result = loginSchema.safeParse(credentials)

        expect(result.success).toBe(false)
        if (!result.success) {
            expect(result.error.issues.some((issue) => issue.path[0] === "password")).toBe(true)
        }
    })
})

describe("signupSchema", () => {
    it("accepts matching passwords within the supported length", () => {
        expect(signupSchema.safeParse({
            email: "user@example.com",
            password: "Tr7$kq!mZx9#pL2vB",
            confirmPassword: "Tr7$kq!mZx9#pL2vB",
        }).success).toBe(true)
    })

    it("rejects a short password", () => {
        const result = signupSchema.safeParse({
            email: "user@example.com",
            password: "short",
            confirmPassword: "short",
        })

        expect(result.success).toBe(false)
        if (!result.success) {
            expect(result.error.issues.some((issue) => issue.path[0] === "password")).toBe(true)
        }
    })

    it("rejects mismatched passwords at the confirmation field", () => {
        const result = signupSchema.safeParse({
            email: "user@example.com",
            password: "Tr7$kq!mZx9#pL2vB",
            confirmPassword: "different",
        })

        expect(result.success).toBe(false)
        if (!result.success) {
            expect(result.error.issues.some((issue) =>
                issue.path[0] === "confirmPassword" && issue.message === "Passwords do not match"
            )).toBe(true)
        }
    })

    it("rejects passwords longer than 100 characters", () => {
        const password = "a".repeat(101)
        expect(signupSchema.safeParse({
            email: "user@example.com",
            password,
            confirmPassword: password,
        }).success).toBe(false)
    })
})

describe("signupSchema password strength", () => {
    it("rejects a weak password at the password field", () => {
        const result = signupSchema.safeParse({
            email: "user@example.com",
            password: "password123",
            confirmPassword: "password123",
        })

        expect(result.success).toBe(false)
        if (!result.success) {
            expect(result.error.issues.some((issue) => issue.path[0] === "password")).toBe(true)
        }
    })

    it("rejects a password longer than the server limit", () => {
        const long = "Tr7$kq!mZx9#pL2vB".repeat(5).slice(0, 73)
        const result = signupSchema.safeParse({
            email: "user@example.com",
            password: long,
            confirmPassword: long,
        })

        expect(result.success).toBe(false)
        if (!result.success) {
            expect(result.error.issues.some((issue) => issue.path[0] === "password")).toBe(true)
        }
    })
})
