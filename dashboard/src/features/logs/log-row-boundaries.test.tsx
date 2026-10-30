// @vitest-environment jsdom
import { cleanup, render } from "@testing-library/react"
import { afterEach, expect, it, vi } from "vitest"

import { LogRow, type LogRowModel } from "./log-row"

afterEach(cleanup)

it("renders nothing for a virtualized index removed from the current log dataset", () => {
    const model: LogRowModel = {
        logs: [],
        lineSelection: null,
        jobStatus: "COMPLETED",
        disableLogInteractions: false,
        parseLog: vi.fn(),
        handleLogRowPointerDown: vi.fn(),
        handleLogRowPointerMove: vi.fn(),
        rowPointerGestureRef: { current: null },
        handleLogRowClick: vi.fn(),
        copyLogLines: vi.fn().mockResolvedValue(undefined),
        copyPermalink: vi.fn().mockResolvedValue(undefined),
    }
    const { container } = render(<LogRow index={2} model={model} />)
    expect(container.childElementCount).toBe(0)
    expect(model.parseLog).not.toHaveBeenCalled()
    expect(model.copyLogLines).not.toHaveBeenCalled()
    expect(model.copyPermalink).not.toHaveBeenCalled()
})
