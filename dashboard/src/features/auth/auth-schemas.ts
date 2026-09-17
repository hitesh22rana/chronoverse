import { z } from "zod"
import { ZxcvbnFactory } from "@zxcvbn-ts/core"
import * as zxcvbnCommon from "@zxcvbn-ts/language-common"

const zxcvbn = new ZxcvbnFactory({
    dictionary: zxcvbnCommon.dictionary,
    graphs: zxcvbnCommon.adjacencyGraphs,
})

export const loginSchema = z.object({
    email: z.email({ message: "Please enter a valid email" }),
    password: z.string().min(1, { message: "Password is required" }),
})

export type LoginValues = z.infer<typeof loginSchema>

// Mirrors the server policy (min 8, max 72, zxcvbn score >= 3 with the email
// as user input). JS and Go zxcvbn differ, so scores can diverge; this check
// is a hint only, the server remains the authority.
export const signupSchema = z
    .object({
        email: z.email({ message: "Please enter a valid email" }),
        password: z
            .string()
            .min(8, { message: "Password must be at least 8 characters" })
            .max(72, { message: "Password must be at most 72 characters" }),
        confirmPassword: z.string().min(1, { message: "Please confirm your password" }),
    })
    .refine((data) => data.password === data.confirmPassword, {
        path: ["confirmPassword"],
        message: "Passwords do not match",
    })
    .refine((data) => zxcvbn.check(data.password, [data.email]).score >= 3, {
        path: ["password"],
        message: "Password is too weak: use a longer passphrase with mixed words, numbers, and symbols",
    })

export type SignupValues = z.infer<typeof signupSchema>
