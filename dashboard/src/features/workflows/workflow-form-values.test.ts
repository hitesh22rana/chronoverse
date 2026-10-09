import { describe, expect, it } from "vitest"
import { serializeWorkflowPayload, type WorkflowConfigurationValues } from "./workflow-form-values"

describe("serializeWorkflowPayload", () => {
    it("turns the heartbeat headers into an object and keeps a filled timeout", () => {
        const payload = serializeWorkflowPayload("HEARTBEAT", {
            heartbeatPayload: {
                endpoint: "https://example.com/health",
                expectedStatusCode: 204,
                headers: [
                    { id: "1", key: "X-Token", value: "secret" },
                    { id: "2", key: "Accept", value: "application/json" },
                ],
                timeout: "10s",
            },
        })

        expect(JSON.parse(payload)).toEqual({
            endpoint: "https://example.com/health",
            expected_status_code: 204,
            headers: { "X-Token": "secret", Accept: "application/json" },
            timeout: "10s",
        })
    })

    it("leaves a header row out when its name was never filled in", () => {
        // The header list starts with an unnamed row as soon as one is added.
        const payload = serializeWorkflowPayload("HEARTBEAT", {
            heartbeatPayload: {
                endpoint: "https://example.com/health",
                expectedStatusCode: 200,
                headers: [
                    { id: "1", key: "", value: "orphan value" },
                    { id: "2", key: "X-Token", value: "secret" },
                ],
                timeout: "",
            },
        })

        expect(JSON.parse(payload)).toEqual({
            endpoint: "https://example.com/health",
            expected_status_code: 200,
            headers: { "X-Token": "secret" },
        })
    })

    it("falls back to an empty heartbeat request when the form has no payload", () => {
        // The update form only fills the payload that matches the workflow kind.
        expect(serializeWorkflowPayload("HEARTBEAT", {})).toBe(
            JSON.stringify({ endpoint: "", expected_status_code: 200, headers: {} }),
        )
    })

    it("omits the container command, variables and timeout when none were set", () => {
        const payload = serializeWorkflowPayload("CONTAINER", {
            containerPayload: { image: "alpine:latest", cmd: [], env: [], timeout: "" },
        })

        expect(JSON.parse(payload)).toEqual({ image: "alpine:latest" })
    })

    it("keeps a container command and splits its variables on the first equals sign", () => {
        const payload = serializeWorkflowPayload("CONTAINER", {
            containerPayload: {
                image: "alpine:latest",
                cmd: ["sh", "-c", "echo hello"],
                env: ["MODE=prod", "TOKEN=abc=def"],
                timeout: "30s",
            },
        })

        expect(JSON.parse(payload)).toEqual({
            image: "alpine:latest",
            cmd: ["sh", "-c", "echo hello"],
            env: { MODE: "prod", TOKEN: "abc=def" },
            timeout: "30s",
        })
    })

    it("skips an untouched variable row and one whose name is empty", () => {
        // Adding a variable row appends an empty entry; a leading "=" is no name either.
        const payload = serializeWorkflowPayload("CONTAINER", {
            containerPayload: { image: "alpine:latest", cmd: [], env: ["", "=orphan"], timeout: "" },
        })

        // Rows that name nothing drop out, so an added-but-unused list sends no variables.
        expect(JSON.parse(payload)).toEqual({ image: "alpine:latest", env: {} })
    })

    it("falls back to an empty container command when the form has no payload", () => {
        expect(serializeWorkflowPayload("CONTAINER", {})).toBe(JSON.stringify({ image: "" }))
    })

    it("sends an empty object for a workflow kind it does not build a payload for", () => {
        // A workflow's kind is whatever the API reports, so an unknown one still saves.
        const values: WorkflowConfigurationValues = {}

        expect(serializeWorkflowPayload("SCHEDULED", values)).toBe("{}")
    })
})

it("parses container env entries without dropping bare names or values", () => {
    const payload = JSON.parse(serializeWorkflowPayload("CONTAINER", {
        containerPayload: {
            image: "alpine",
            cmd: [],
            env: ["FOO=bar=baz", "BAZ", "EMPTY="],
            timeout: "",
        },
    }))
    expect(payload.env).toEqual({ FOO: "bar=baz", BAZ: "", EMPTY: "" })
})
