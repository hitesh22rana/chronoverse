import { describe, expect, it } from "vitest"

import { isWorkflowDetailsReady } from "./workflow-query-state"

describe("isWorkflowDetailsReady", () => {
    it.each([
        { fetchStatus: "idle", error: null, ready: true },
        { fetchStatus: "idle", error: new Error("boom"), ready: false },
        { fetchStatus: "fetching", error: null, ready: false },
        // An offline fetch pauses without surfacing an error, so fetchStatus
        // alone must not unlock the stale cached values.
        { fetchStatus: "paused", error: null, ready: false },
        { fetchStatus: "paused", error: new Error("boom"), ready: false },
    ] as const)("$fetchStatus with $error unlocks: $ready", ({ fetchStatus, error, ready }) => {
        expect(isWorkflowDetailsReady(fetchStatus, error)).toBe(ready)
    })
})
