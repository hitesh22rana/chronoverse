// @vitest-environment jsdom
/**
 * Mounted chart tests cover the generated stylesheet and the tooltip contract
 * the analytics charts rely on. Only the measurements jsdom cannot make are
 * stubbed; recharts itself renders for real.
 */
import type { ReactNode } from "react"
import { cleanup, render, screen, within } from "@testing-library/react"
import { afterEach, beforeEach, expect, it, vi } from "vitest"

import { ChartContainer, ChartStyle, ChartTooltipContent, type ChartConfig } from "@/components/ui/chart"

type TooltipProps = Parameters<typeof ChartTooltipContent>[0]
type TooltipItem = NonNullable<TooltipProps["payload"]>[number]

const seriesConfig = {
    total_jobs: { label: "Terminal jobs", color: "var(--chart-2)" },
    total_joblogs: { label: "Generated logs", theme: { light: "#111111", dark: "#eeeeee" } },
} satisfies ChartConfig

/** A tooltip entry as recharts hands it to the content: key, name, value and colour. */
const entry = (overrides: Partial<TooltipItem> = {}): TooltipItem => ({
    dataKey: "total_jobs",
    name: "total_jobs",
    value: 12345,
    color: "#0f172a",
    graphicalItemId: "series-0",
    ...overrides,
})

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
    // An unmeasured container renders nothing at all, so chart children never
    // mount; give it the box a browser would.
    Element.prototype.getBoundingClientRect = function () {
        const element = this as Element
        return element.classList.contains("recharts-responsive-container")
            ? CHART_RECT
            : originalGetBoundingClientRect.call(element)
    }
}

function renderTooltip(payload: TooltipItem[], props: Partial<TooltipProps> = {}, config: ChartConfig = seriesConfig) {
    return render(
        <ChartContainer config={config}>
            <ChartTooltipContent active payload={payload} {...props} />
        </ChartContainer>,
    )
}

/** The rendered row of the nth payload entry, in payload order. */
function rowAt(container: HTMLElement, index: number) {
    const row = container.querySelectorAll("div.flex.w-full.flex-wrap")[index]
    if (!row) throw new Error(`no tooltip row at ${index}`)
    return row as HTMLElement
}

const indicatorOf = (row: HTMLElement) =>
    row.querySelector<HTMLElement>("[style*='--color-bg']")

/** The heading the tooltip puts above its rows, if it has one. */
const tooltipHeading = (container: HTMLElement) =>
    container.querySelector<HTMLElement>("div.font-medium:not(.font-mono)")

beforeEach(() => {
    installChartLayout()
})

afterEach(() => {
    cleanup()
    Element.prototype.getBoundingClientRect = originalGetBoundingClientRect
    vi.unstubAllGlobals()
    vi.restoreAllMocks()
})

it("writes a colour variable per series under both theme selectors", () => {
    const { container } = render(<ChartStyle id="chart-jobs" config={seriesConfig} />)

    const css = container.querySelector("style")?.innerHTML ?? ""
    expect(css).toContain("[data-chart=chart-jobs]")
    expect(css).toContain(".dark [data-chart=chart-jobs]")
    expect(css).toContain("--color-total_jobs: var(--chart-2)")
    // A themed series resolves per theme instead of falling back to one colour.
    expect(css).toContain("--color-total_joblogs: #111111")
    expect(css).toContain("--color-total_joblogs: #eeeeee")
})

it("generates its own chart id when none is given", () => {
    const { container } = render(
        <ChartContainer config={seriesConfig}>
            <div />
        </ChartContainer>,
    )

    const id = container.querySelector("[data-slot=chart]")?.getAttribute("data-chart")
    expect(id).toMatch(/^chart-[A-Za-z0-9_]+$/)
    expect(container.querySelector("style")?.innerHTML).toContain(`[data-chart=${id}]`)
})

it("emits no stylesheet when no series declares a colour", () => {
    const { container } = render(
        <ChartStyle id="chart-jobs" config={{ total_jobs: { label: "Terminal jobs" } }} />,
    )

    expect(container.querySelector("style")).toBeNull()
})

it("declares nothing for a themed series whose themes are both empty", () => {
    const { container } = render(
        <ChartStyle id="chart-jobs" config={{ total_jobs: { theme: { light: "", dark: "" } } }} />,
    )

    // The stylesheet is still emitted, with nothing to put in it.
    const style = container.querySelector("style")
    expect(style).not.toBeNull()
    expect(style?.innerHTML).not.toContain("--color-total_jobs")
})

it("draws nothing until the tooltip is active with an entry", () => {
    const { container } = renderTooltip([entry()], { active: false })

    expect(tooltipHeading(container)).toBeNull()
    cleanup()
    renderTooltip([], { active: true })

    expect(screen.queryByText("Terminal jobs")).toBeNull()
})

it("labels a series from the chart config and formats its value", () => {
    const { container } = renderTooltip([entry()])

    // The series name heads the tooltip and names its own row.
    expect(tooltipHeading(container)?.textContent).toBe("Terminal jobs")
    expect(within(rowAt(container, 0)).getByText("Terminal jobs")).toBeTruthy()
    expect(screen.getByText("12,345")).toBeTruthy()
    expect(indicatorOf(rowAt(container, 0))?.style.getPropertyValue("--color-bg")).toBe("#0f172a")
})

it("hides the tooltip label when the chart asks it to", () => {
    const { container } = renderTooltip([entry()], { hideLabel: true })

    expect(tooltipHeading(container)).toBeNull()
    expect(within(rowAt(container, 0)).getByText("Terminal jobs")).toBeTruthy()
})

it("reads the tooltip's own label out of the config", () => {
    const { container } = renderTooltip([entry()], { label: "total_jobs" })

    expect(tooltipHeading(container)?.textContent).toBe("Terminal jobs")
    cleanup()
    const unknown = renderTooltip([entry()], { label: "not_in_config" })

    // A label the config does not describe still names the tooltip.
    expect(tooltipHeading(unknown.container)?.textContent).toBe("not_in_config")
})

it("resolves the entry's label through labelKey", () => {
    const { container } = renderTooltip([entry({ dataKey: undefined, name: "total_jobs" })], { labelKey: "name" })

    expect(tooltipHeading(container)?.textContent).toBe("Terminal jobs")
})

it("names a series that carries no data key by its own name", () => {
    const { container } = renderTooltip([entry({ dataKey: undefined })])

    // Only the name can find the series in the config, so it has to be used.
    expect(tooltipHeading(container)?.textContent).toBe("Terminal jobs")
})

it("names a series that carries no name by its data key", () => {
    const { container } = renderTooltip([entry({ name: undefined })])

    expect(tooltipHeading(container)?.textContent).toBe("Terminal jobs")
    expect(within(rowAt(container, 0)).getByText("Terminal jobs")).toBeTruthy()
})

it("resolves the entry's label through its own datum", () => {
    const { container } = renderTooltip([entry({ dataKey: "count", payload: { count: "total_joblogs" } })], {
        labelKey: "count",
    })

    expect(tooltipHeading(container)?.textContent).toBe("Generated logs")
})

it("falls back to the entry's own name when the config has nothing for it", () => {
    const { container } = renderTooltip([entry({ dataKey: "raw_series", name: "raw series" })], {}, {
        total_jobs: seriesConfig.total_jobs,
    })

    expect(within(rowAt(container, 0)).getByText("raw series")).toBeTruthy()
    // Without a configured label there is nothing to head the tooltip with.
    expect(tooltipHeading(container)).toBeNull()
})

it("hands the label to labelFormatter instead of printing it", () => {
    const labelFormatter = vi.fn(
        (value: ReactNode, payload: readonly unknown[]) => `${value} of ${payload.length}`,
    )
    const { container } = renderTooltip([entry()], { labelFormatter, labelClassName: "uppercase" })

    expect(labelFormatter).toHaveBeenCalledWith("Terminal jobs", [entry()])
    expect(tooltipHeading(container)?.textContent).toBe("Terminal jobs of 1")
    expect(tooltipHeading(container)?.className).toContain("uppercase")
})

it("nests the label inside a lone line-indicator entry", () => {
    const { container } = renderTooltip([entry()], { indicator: "line" })

    // One line-indicator entry carries its own label instead of repeating it.
    const row = rowAt(container, 0)
    expect(tooltipHeading(container)?.closest("div.flex.w-full")).toBe(row)
    expect(indicatorOf(row)?.className).toContain("w-1")
})

it("draws a dashed indicator inside a nested label", () => {
    const { container } = renderTooltip([entry()], { indicator: "dashed" })

    const indicator = indicatorOf(rowAt(container, 0))
    expect(indicator?.className).toContain("border-dashed")
    expect(indicator?.className).toContain("my-0.5")
})

it("keeps a dashed indicator beside the label when several series share it", () => {
    const { container } = renderTooltip(
        [entry(), entry({ dataKey: "total_joblogs", name: "total_joblogs", value: 7 })],
        { indicator: "dashed" },
    )

    // Two entries cannot share a nested label, so the heading stays put.
    expect(tooltipHeading(container)?.closest("div.flex.w-full")).toBeNull()
    const indicator = indicatorOf(rowAt(container, 1))
    expect(indicator?.className).toContain("border-dashed")
    expect(indicator?.className).not.toContain("my-0.5")
})

it("omits the indicator when the chart asks it to", () => {
    const { container } = renderTooltip([entry()], { hideIndicator: true })

    expect(indicatorOf(rowAt(container, 0))).toBeNull()
})

it("takes the indicator colour from the prop, the datum fill, then the series", () => {
    const fromProp = renderTooltip([entry({ payload: { fill: "var(--chart-3)" } })], { color: "#ff0000" })
    expect(indicatorOf(rowAt(fromProp.container, 0))?.style.getPropertyValue("--color-bg")).toBe("#ff0000")
    cleanup()
    const fromFill = renderTooltip([entry({ payload: { fill: "var(--chart-3)" } })])
    expect(indicatorOf(rowAt(fromFill.container, 0))?.style.getPropertyValue("--color-bg")).toBe("var(--chart-3)")
    cleanup()
    const fromSeries = renderTooltip([entry({ payload: {} })])
    expect(indicatorOf(rowAt(fromSeries.container, 0))?.style.getPropertyValue("--color-bg")).toBe("#0f172a")
})

it("shows the configured icon in place of the indicator", () => {
    function TokenIcon() {
        return <span data-testid="series-icon" />
    }
    render(
        <ChartContainer config={{ total_jobs: { label: "Terminal jobs", icon: TokenIcon } }}>
            <ChartTooltipContent active payload={[entry()]} />
        </ChartContainer>,
    )

    expect(screen.getByTestId("series-icon")).toBeTruthy()
    expect(document.querySelector("[style*='--color-bg']")).toBeNull()
})

it("lets a formatter replace an entry's whole row", () => {
    const formatter = vi.fn(
        (value: unknown, name: unknown, _item: unknown, index: number) => `${name}=${value}#${index}`,
    )
    const { container } = renderTooltip([entry()], { formatter })

    expect(formatter).toHaveBeenCalledWith(12345, "total_jobs", expect.anything(), 0, undefined)
    expect(within(rowAt(container, 0)).getByText("total_jobs=12345#0")).toBeTruthy()
    expect(screen.queryByText("12,345")).toBeNull()
})

it("keeps the default row when a formatter has no value to format", () => {
    const formatter = vi.fn(() => "replaced")
    const { container } = renderTooltip([entry({ value: undefined })], { formatter })

    expect(screen.queryByText("replaced")).toBeNull()
    expect(within(rowAt(container, 0)).getByText("Terminal jobs")).toBeTruthy()
})

it("skips the entries the chart marked as carrying nothing", () => {
    const { container } = renderTooltip([entry(), entry({ name: "skipped", type: "none" })])

    expect(container.querySelectorAll("div.flex.w-full.flex-wrap")).toHaveLength(1)
    expect(screen.queryByText("skipped")).toBeNull()
})

it("prints a non-numeric value as text and omits an absent one", () => {
    const { container } = renderTooltip([
        entry({ dataKey: "Alpha", name: "Alpha", value: "n/a" }),
        entry({ dataKey: "Beta", name: "Beta", value: undefined }),
    ])

    expect(within(rowAt(container, 0)).getByText("n/a")).toBeTruthy()
    expect(rowAt(container, 1).querySelector(".font-mono")).toBeNull()
})

it("refuses to render a tooltip that has no chart around it", () => {
    vi.spyOn(console, "error").mockImplementation(() => {})

    expect(() => render(<ChartTooltipContent active payload={[entry()]} />)).toThrow(
        /useChart must be used within a <ChartContainer \/>/,
    )
})
