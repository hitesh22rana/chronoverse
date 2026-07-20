import { z } from "zod"

export const loginSchema = z.object({
    email: z.email({ message: "Please enter a valid email" }),
    password: z.string().min(1, { message: "Password is required" }),
})

export type LoginValues = z.infer<typeof loginSchema>

export const signupSchema = z
    .object({
        email: z.email({ message: "Please enter a valid email" }),
        password: z
            .string()
            .min(8, { message: "Password must be at least 8 characters" })
            .max(100, { message: "Password must be at most 100 characters" }),
        confirmPassword: z.string().min(1, { message: "Please confirm your password" }),
    })
    .refine((data) => data.password === data.confirmPassword, {
        path: ["confirmPassword"],
        message: "Passwords do not match",
    })

export type SignupValues = z.infer<typeof signupSchema>
