import { z } from "zod"
import zxcvbn from "zxcvbn"

// Server counts runes, string.length counts UTF-16 units.
const runeLength = (s: string) => [...s].length

const MIN_PASSWORD_RUNES = 8
const MAX_PASSWORD_RUNES = 72

const passwordSchema = z
    .string()
    .min(1, { message: "Password is required" })
    .refine((s) => s.length === 0 || runeLength(s) >= MIN_PASSWORD_RUNES, {
        message: "Password must be at least 8 characters",
    })
    .refine((s) => runeLength(s) <= MAX_PASSWORD_RUNES, {
        message: "Password must be at most 72 characters",
    })

export const loginSchema = z.object({
    email: z.email({ message: "Please enter a valid email" }),
    password: passwordSchema,
})

export type LoginValues = z.infer<typeof loginSchema>

// Same policy as the server (trustelem port of dropbox zxcvbn 4.4.2);
// the server remains the authority.
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
    .refine(
        (data) => {
            const len = runeLength(data.password)
            // Length errors are reported above; scoring rejected input
            // would block the UI for seconds on long pastes.
            if (len < MIN_PASSWORD_RUNES || len > MAX_PASSWORD_RUNES) return true
            return zxcvbn(data.password, [data.email]).score >= 3
        },
        {
            path: ["password"],
            message: "Password is too weak: use a longer passphrase with mixed words, numbers, and symbols",
        },
    )

export type SignupValues = z.infer<typeof signupSchema>
