import { describe, expect, it } from "vitest"

import {
    normalizeUserAnalytics,
    normalizeWorkflowAnalytics,
} from "./analytics-data"

describe("analytics response normalization", () => {
    it("turns an empty protobuf JSON response into explicit zero aggregates", () => {
        expect(normalizeUserAnalytics({})).toEqual({
            total_workflows: 0,
            total_jobs: 0,
            total_joblogs: 0,
            total_job_execution_duration: 0,
            workflow_kinds: [],
            top_workflows: [],
        })
    })

    it("defaults omitted nested counters while preserving their identities", () => {
        expect(normalizeUserAnalytics({
            total_jobs: 3,
            workflow_kinds: [{ kind: "CONTAINER", total_jobs: 3 }],
            top_workflows: [{
                workflow_id: "workflow-id",
                workflow_name: "Example",
                kind: "CONTAINER",
                total_jobs: 3,
            }],
        })).toMatchObject({
            total_workflows: 0,
            total_jobs: 3,
            total_joblogs: 0,
            workflow_kinds: [{
                kind: "CONTAINER",
                total_workflows: 0,
                total_jobs: 3,
                total_joblogs: 0,
                total_job_execution_duration: 0,
            }],
            top_workflows: [{
                workflow_id: "workflow-id",
                total_jobs: 3,
                total_joblogs: 0,
                total_job_execution_duration: 0,
            }],
        })
    })

    it("drops unusable collection entries and clamps invalid counters to zero", () => {
        expect(normalizeUserAnalytics({
            total_jobs: Number.NaN,
            total_joblogs: -1,
            workflow_kinds: [null, { total_jobs: 1 }],
            top_workflows: [null, { workflow_name: "Missing ID" }],
        })).toEqual({
            total_workflows: 0,
            total_jobs: 0,
            total_joblogs: 0,
            total_job_execution_duration: 0,
            workflow_kinds: [],
            top_workflows: [],
        })
    })

    it("normalizes an empty workflow response with the requested workflow ID", () => {
        expect(normalizeWorkflowAnalytics({}, "workflow-id")).toEqual({
            workflow_id: "workflow-id",
            total_jobs: 0,
            total_joblogs: 0,
            total_job_execution_duration: 0,
        })
    })
})
