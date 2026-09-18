import { z } from "zod"
import zxcvbn from "zxcvbn"

// Code-point length matches the server validator, which counts runes
// (utf8.RuneCountInString), while string.length counts UTF-16 units.
const runeLength = (s: string) => [...s].length

const passwordSchema = z
    .string()
    .min(1, { message: "Password is required" })
    .refine((s) => s.length === 0 || runeLength(s) >= 8, {
        message: "Password must be at least 8 characters",
    })
    .refine((s) => runeLength(s) <= 72, {
        message: "Password must be at most 72 characters",
    })

export const loginSchema = z.object({
    email: z.email({ message: "Please enter a valid email" }),
    password: passwordSchema,
})

export type LoginValues = z.infer<typeof loginSchema>

// Mirrors the server policy (min 8, max 72, zxcvbn score >= 3 with the email
// as user input) using the same zxcvbn lineage as the backend
// (dropbox 4.4.2 / trustelem port); the server remains the authority.
export const signupSchema = z
    .object({
        email: z.email({ message: "Please enter a valid email" }),
        password: passwordSchema,
        confirmPassword: z.string().min(1, { message: "Please confirm your password" }),
    })
    .refine((data) => data.password === data.confirmPassword, {
        path: ["confirmPassword"],
        message: "Passwords do not match",
    })
    .refine((data) => zxcvbn(data.password, [data.email]).score >= 3, {
        path: ["password"],
        message: "Password is too weak: use a longer passphrase with mixed words, numbers, and symbols",
    })

export type SignupValues = z.infer<typeof signupSchema>
