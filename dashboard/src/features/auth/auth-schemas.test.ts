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
})

describe("signupSchema", () => {
    it("accepts matching passwords within the supported length", () => {
        expect(signupSchema.safeParse({
            email: "user@example.com",
            password: "password",
            confirmPassword: "password",
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
            password: "password",
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
