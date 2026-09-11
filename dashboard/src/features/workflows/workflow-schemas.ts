import { z } from "zod"
import { type Duration, parseDuration } from "@alwatr/parse-duration"

const baseWorkflowSchema = z.object({
    name: z.string().trim().min(3, "Name must be at least 3 characters").max(50, "Name must be at most 50 characters"),
    interval: z.union([
        z.string().trim().refine(val => val === "" || /^\d+$/.test(val), {
            message: "Please enter a valid number"
        }),
        z.number()
    ])
        .transform(val => val === "" ? undefined : Number(val))
        .refine(val => val === undefined || (val >= 1 && val <= 10080), {
            message: "Must be between 1 and 10080 minutes (1 week)"
        }),
    maxConsecutiveJobFailuresAllowed: z.coerce.number().int().min(3).max(100).default(3)
})

const heartbeatPayloadSchema = z.object({
    endpoint: z.url().trim().refine(val => val !== "", {
        message: "Please enter a valid URL"
    }),
    expectedStatusCode: z.coerce.number().int().min(100).max(599).refine(val => val >= 100 && val <= 599, {
        message: "Expected status code must be between 100 and 599"
    }),
    headers: z.array(
        z.object({
            key: z.string().trim().min(1, "Header key is required"),
            value: z.string().trim()
        })
    ).default([]),
    timeout: z.string().default("")
        .refine(val => {
            if (!val) return true
            try {
                const parsed = parseDuration(val as unknown as Duration, 's')
                return parsed > 0 && parsed <= 300
            } catch {
                return false
            }
        }, "Timeout must be a valid duration (e.g., '30s', '1m') max up to 5 minutes")
})

const containerPayloadSchema = z.object({
    image: z.string().trim().min(1, "Container image is required"),
    cmd: z.array(z.string().trim())
        .optional()
        .default([])
        .transform(val => val?.filter(item => item !== "") || []),
    env: z.array(z.string().trim())
        .optional()
        .default([])
        .transform(val => val?.filter(item => item !== "") || []),
    timeout: z.string().default("")
        .refine(val => {
            if (!val) return true
            try {
                const parsed = parseDuration(val as unknown as Duration, 's')
                return parsed > 0 && parsed <= 3600
            } catch {
                return false
            }
        }, "Timeout must be a valid duration (e.g., '30s', '5m') max up to 1 hour")
})

const baseCreateWorkflowSchema = baseWorkflowSchema.extend({
    retainLogs: z.boolean().default(true),
})

const heartbeatWorkflowSchema = baseCreateWorkflowSchema.extend({
    kind: z.literal("HEARTBEAT"),
    heartbeatPayload: heartbeatPayloadSchema.extend({
        expectedStatusCode: heartbeatPayloadSchema.shape.expectedStatusCode.default(200),
    }),
}).transform(data => ({
    ...data,
    retainLogs: false // HEARTBEAT workflows always have log_retention as false
}))

const containerWorkflowSchema = baseCreateWorkflowSchema.extend({
    kind: z.literal("CONTAINER"),
    containerPayload: containerPayloadSchema,
})

export const createWorkflowSchema = z.discriminatedUnion("kind", [
    heartbeatWorkflowSchema,
    containerWorkflowSchema
])

export const updateWorkflowSchema = baseWorkflowSchema.extend({
    maxConsecutiveJobFailuresAllowed: baseWorkflowSchema.shape.maxConsecutiveJobFailuresAllowed.refine(val => val >= 3, {
        message: "Maximum consecutive job failures allowed must be at least 3"
    }),
    heartbeatPayload: heartbeatPayloadSchema.optional(),
    containerPayload: containerPayloadSchema.optional(),
})
