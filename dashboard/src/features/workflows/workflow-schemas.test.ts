import { expect, it } from "vitest"
import { createWorkflowSchema, updateWorkflowSchema } from "./workflow-schemas"

const base = { name: " My workflow ", interval: "5" }
const heartbeat = { endpoint: "https://example.com", expectedStatusCode: "200" }

it("preserves create-only defaults and update payload optionality", () => {
    expect(createWorkflowSchema.parse({ ...base, kind: "HEARTBEAT", retainLogs: true, heartbeatPayload: { endpoint: heartbeat.endpoint } })).toEqual({
        name: "My workflow", interval: 5, maxConsecutiveJobFailuresAllowed: 3, kind: "HEARTBEAT", retainLogs: false,
        heartbeatPayload: { endpoint: heartbeat.endpoint, expectedStatusCode: 200, headers: [], timeout: "" },
    })
    expect(createWorkflowSchema.parse({ ...base, kind: "CONTAINER", containerPayload: { image: " alpine " } })).toMatchObject({
        retainLogs: true, containerPayload: { image: "alpine", cmd: [], env: [], timeout: "" },
    })
    expect(updateWorkflowSchema.parse(base)).toEqual({ name: "My workflow", interval: 5, maxConsecutiveJobFailuresAllowed: 3 })
    expect(updateWorkflowSchema.safeParse({ ...base, heartbeatPayload: { endpoint: heartbeat.endpoint } }).success).toBe(false)
    expect(createWorkflowSchema.safeParse({ ...base, kind: "HEARTBEAT" }).success).toBe(false)
    expect(createWorkflowSchema.safeParse({ ...base, kind: "OTHER" }).success).toBe(false)
})

it.each([createWorkflowSchema, updateWorkflowSchema])("preserves shared validation and normalization %#", (schema) => {
    const input = { ...base, kind: "HEARTBEAT", heartbeatPayload: heartbeat }
    expect(schema.parse({ ...input, interval: "" }).interval).toBeUndefined()
    for (const interval of ["1", "10080"]) {
        expect(schema.parse({ ...input, interval }).interval).toBe(Number(interval))
    }
    for (const interval of ["0", "10081", "5m", "1.5"]) {
        expect(schema.safeParse({ ...input, interval }).success).toBe(false)
    }
    for (const name of [" a ", "a".repeat(51)]) {
        expect(schema.safeParse({ ...input, name }).success).toBe(false)
    }
    for (const maxConsecutiveJobFailuresAllowed of [2, 101, 3.5]) {
        expect(schema.safeParse({ ...input, maxConsecutiveJobFailuresAllowed }).success).toBe(false)
    }
    for (const expectedStatusCode of [100, 599]) {
        expect(schema.safeParse({ ...input, heartbeatPayload: { ...heartbeat, expectedStatusCode } }).success).toBe(true)
    }
    for (const expectedStatusCode of [99, 600, 200.5]) {
        expect(schema.safeParse({ ...input, heartbeatPayload: { ...heartbeat, expectedStatusCode } }).success).toBe(false)
    }
    for (const payload of [{ ...heartbeat, endpoint: "invalid" }, { ...heartbeat, headers: [{ key: " ", value: "" }] }]) {
        expect(schema.safeParse({ ...input, heartbeatPayload: payload }).success).toBe(false)
    }
    for (const [kind, field, payload, limit] of [
        ["HEARTBEAT", "heartbeatPayload", heartbeat, 300],
        ["CONTAINER", "containerPayload", { image: "alpine" }, 3600],
    ] as const) {
        for (const timeout of ["", `${limit}s`]) {
            expect(schema.safeParse({ ...base, kind, [field]: { ...payload, timeout } }).success).toBe(true)
        }
        for (const timeout of ["0s", `${limit + 1}s`, "invalid"]) {
            expect(schema.safeParse({ ...base, kind, [field]: { ...payload, timeout } }).success).toBe(false)
        }
    }
    expect(schema.parse({ ...base, kind: "CONTAINER", containerPayload: { image: " alpine ", cmd: [" echo ", " "], env: [" A=1 ", ""] } })).toMatchObject({
        containerPayload: { image: "alpine", cmd: ["echo"], env: ["A=1"], timeout: "" },
    })
    expect(schema.safeParse({ ...base, kind: "CONTAINER", containerPayload: { image: " " } }).success).toBe(false)
})
