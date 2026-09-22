import { expect, it } from "vitest"
import { serializeWorkflowPayload } from "./workflow-form-values"

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
