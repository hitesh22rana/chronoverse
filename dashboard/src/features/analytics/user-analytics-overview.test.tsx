// @vitest-environment jsdom
/**
 * Mounted analytics tests render the real charts. jsdom has no layout, so the
 * recharts container is handed the box a browser would measure; nothing else
 * about the chart is stubbed.
 */
import { cleanup, render, screen } from "@testing-library/react"
import { afterEach, beforeEach, expect, it, vi } from "vitest"

import { UserAnalyticsOverview } from "@/features/analytics/user-analytics-overview"
import type { UserAnalytics } from "@/features/analytics/types"

/** Chart size recharts cannot measure for itself in jsdom. */
const CHART_RECT = {
    width: 800, height: 400, top: 0, left: 0, right: 800, bottom: 400, x: 0, y: 0,
    toJSON: () => ({}),
} as DOMRect

const originalGetBoundingClientRect = Element.prototype.getBoundingClientRect

function installChartLayout() {
    // Recharts observes its container with a ResizeObserver, which jsdom lacks.
    if (!("ResizeObserver" in globalThis)) {
        class ResizeObserverStub {
            observe() {}
            unobserve() {}
            disconnect() {}
        }
        vi.stubGlobal("ResizeObserver", ResizeObserverStub)
    }
    // Without a measured size the container renders nothing at all, so the
    // charts never draw; give it the box a browser would.
    Element.prototype.getBoundingClientRect = function () {
        const element = this as Element
        return element.classList.contains("recharts-responsive-container")
            ? CHART_RECT
            : originalGetBoundingClientRect.call(element)
    }
}

const analytics: UserAnalytics = {
    total_workflows: 4,
    total_jobs: 12,
    total_joblogs: 30,
    total_job_execution_duration: 3660,
    workflow_kinds: [
        {
            kind: "CONTAINER",
            total_workflows: 2,
            total_jobs: 8,
            total_joblogs: 20,
            total_job_execution_duration: 2400,
        },
        {
            kind: "HEARTBEAT",
            total_workflows: 2,
            total_jobs: 4,
            total_joblogs: 10,
            total_job_execution_duration: 1260,
        },
    ],
    top_workflows: [
        {
            workflow_id: "w1",
            workflow_name: "Nightly container rebuild and publish",
            kind: "CONTAINER",
            total_jobs: 8,
            total_joblogs: 20,
            total_job_execution_duration: 2400,
        },
        {
            workflow_id: "w2",
            workflow_name: "API heartbeat",
            kind: "HEARTBEAT",
            total_jobs: 4,
            total_joblogs: 10,
            total_job_execution_duration: 1260,
        },
    ],
}

function renderOverview(data: UserAnalytics) {
    return render(<UserAnalyticsOverview analytics={data} />)
}

/** The headline card for one metric, with its stated total and derived rate. */
function metric(label: string) {
    const card = screen.getByText(label).closest("[data-slot=card]")?.querySelector("[data-slot=card-content]")
    if (!card) throw new Error(`no metric card for ${label}`)
    const [value, helper] = card.querySelectorAll("p")
    return { value: value?.textContent, helper: helper?.textContent }
}

/** The workflow names the ranking chart puts on its axis, in the order drawn. */
const axisLabels = (container: HTMLElement) =>
    Array.from(container.querySelectorAll(".recharts-cartesian-axis-tick-value")).map(
        (tick) => tick.textContent,
    )

beforeEach(() => {
    installChartLayout()
})

afterEach(() => {
    cleanup()
    Element.prototype.getBoundingClientRect = originalGetBoundingClientRect
    vi.unstubAllGlobals()
})

it("states the account totals and the rates derived from them", () => {
    renderOverview(analytics)

    expect(metric("Workflows")).toEqual({ value: "4", helper: "~3 jobs per workflow" })
    expect(metric("Terminal jobs")).toEqual({ value: "12", helper: "5m 5s average runtime" })
    expect(metric("Generated logs")).toEqual({ value: "30", helper: "~3 logs per job" })
    expect(metric("Execution time")).toEqual({
        value: "1h 1m",
        helper: "Across 12 terminal jobs",
    })
})

it("draws the workload mix with a total in the middle and a kind per slice", () => {
    const { container } = renderOverview(analytics)

    // The pie's own label states the total the slices add up to.
    const centre = Array.from(container.querySelectorAll("tspan")).map((node) => node.textContent)
    expect(centre).toContain("12")
    expect(centre).toContain("terminal jobs")
    // The legend repeats every kind with its job count.
    const kinds = Array.from(container.querySelectorAll(".truncate.text-sm")).map(
        (row) => row.textContent,
    )
    expect(kinds).toEqual(["Container", "Heartbeat"])
    expect(Array.from(container.querySelectorAll(".font-mono")).map((row) => row.textContent)).toEqual([
        "8",
        "4",
    ])
})

it("ranks the busiest workflows and shortens a name that will not fit", () => {
    const { container } = renderOverview(analytics)

    expect(screen.getByText("Most active workflows")).toBeTruthy()
    expect(screen.getByText("Top 10 ranked by durable terminal-job count")).toBeTruthy()
    expect(container.querySelectorAll(".recharts-bar-rectangle")).toHaveLength(2)
    // The long name is elided for the axis; the short one is left alone.
    expect(axisLabels(container)).toEqual(["Nightly container…", "API heartbeat"])
})

it("keeps only the ten busiest workflows on the ranking chart", () => {
    const ranked = Array.from({ length: 12 }, (_, index) => ({
        workflow_id: `w${index}`,
        workflow_name: `Workflow ${index + 1}`,
        kind: "CONTAINER",
        total_jobs: 12 - index,
        total_joblogs: index,
        total_job_execution_duration: index,
    }))
    const { container } = renderOverview({ ...analytics, top_workflows: ranked })

    expect(container.querySelectorAll(".recharts-bar-rectangle")).toHaveLength(10)
    expect(axisLabels(container)).toHaveLength(10)
    expect(axisLabels(container)).toContain("Workflow 10")
    expect(axisLabels(container)).not.toContain("Workflow 11")
})

it("leaves out a workflow kind that has not finished a job", () => {
    renderOverview({
        ...analytics,
        workflow_kinds: [{ ...analytics.workflow_kinds[0], total_jobs: 0 }],
    })

    expect(screen.getByText("No terminal job activity yet")).toBeTruthy()
    // The ranking chart has its own activity to show.
    expect(screen.queryByText("No workflow activity yet")).toBeNull()
})

it("says so instead of averaging nothing when no job has run", () => {
    renderOverview({
        total_workflows: 0,
        total_jobs: 0,
        total_joblogs: 0,
        total_job_execution_duration: 0,
        workflow_kinds: [],
        top_workflows: [],
    })

    expect(screen.getByText("No terminal job activity yet")).toBeTruthy()
    expect(screen.getByText("No workflow activity yet")).toBeTruthy()
    expect(screen.getByText("No terminal jobs recorded")).toBeTruthy()
    expect(screen.getByText("No logs generated")).toBeTruthy()
    expect(screen.getByText("0s")).toBeTruthy()
})
