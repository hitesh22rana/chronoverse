// @vitest-environment jsdom
/**
 * Mounted interaction tests for the log viewer: deep-link line selection,
 * scroll correction, paging and dataset changes.
 *
 * Boundaries only: the transport (`@/lib/api/client`), the Next.js navigation
 * hooks, `sonner`, and the virtualization/scroll surface (`react-virtuoso`),
 * which jsdom cannot lay out. `LogsViewer`, `useJobLogs`, `useLogSelection`,
 * `LogRow` and react-query all run for real, so the assertions are about what a
 * user sees (highlighted rows, permalink fragment, paged rows, warnings) and
 * about the scroll commands the viewer issues.
 */
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { useEffect, useState } from "react"
import { afterEach, beforeEach, expect, it, vi } from "vitest"
import type { MockInstance } from "vitest"

import { LogsViewer } from "./logs-viewer"

const mocks = vi.hoisted(() => ({
    pathname: "/workflows/w1/jobs/j1",
    search: "",
    push: vi.fn(),
    replace: vi.fn(),
    fetchApi: vi.fn(),
    fetchApiJson: vi.fn(),
    toast: { error: vi.fn(), success: vi.fn(), warning: vi.fn() },
    notify: null as null | (() => void),
    virtuosoProps: null as null | Record<string, (..._args: unknown[]) => unknown>,
    scrollToIndex: vi.fn(),
    scrollIntoView: vi.fn(),
    scrollTo: vi.fn(),
    objectUrls: [] as string[],
    anchorClick: null as unknown as MockInstance<() => void>,
}))

vi.mock("react-virtuoso", async () => {
    const React = await import("react")
    const Virtuoso = React.forwardRef(function Virtuoso(props: any, ref: React.Ref<unknown>) {
        mocks.virtuosoProps = props
        React.useImperativeHandle(ref, () => ({
            scrollToIndex: mocks.scrollToIndex,
            scrollIntoView: mocks.scrollIntoView,
            scrollTo: mocks.scrollTo,
        }), [])
        const rows: React.ReactElement[] = []
        for (let index = 0; index < props.totalCount; index += 1) {
            rows.push(React.createElement("div", { key: index, "data-virtuoso-row": index }, props.itemContent(index, index)))
        }
        return React.createElement("div", { "data-testid": "virtuoso-list" }, rows)
    })
    return { Virtuoso }
})

vi.mock("next/navigation", () => ({
    usePathname: () => mocks.pathname,
    useRouter: () => ({ push: mocks.push, replace: mocks.replace }),
    useSearchParams: () => new URLSearchParams(mocks.search),
}))

vi.mock("sonner", () => ({ toast: mocks.toast, Toaster: () => null }))

vi.mock("@/lib/api/client", () => ({
    fetchApi: mocks.fetchApi,
    fetchApiJson: mocks.fetchApiJson,
    createIdempotencyKey: () => "idempotency-key",
}))

const workflow = {
    id: "w1",
    name: "Retention job",
    kind: "CONTAINER",
    payload: JSON.stringify({ image: "alpine:latest" }),
    build_status: "COMPLETED",
    interval: 5,
    max_consecutive_job_failures_allowed: 3,
    log_retention: true,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
}

const log = (sequenceNum: number, label: string) => ({
    event_id: `e:${sequenceNum}`,
    sequence_num: sequenceNum,
    message: `${label} ${sequenceNum}`,
    timestamp: new Date(Date.UTC(2026, 0, 1, 0, 0, sequenceNum)).toISOString(),
    stream: "stdout",
})

/** Newest-first retained pages, as the jobs service returns them. */
const newerPage = {
    id: "j1",
    workflow_id: "w1",
    logs: Array.from({ length: 20 }, (_, index) => log(40 - index, "retained")),
    cursor: "page-2",
}
const olderPage = {
    id: "j1",
    workflow_id: "w1",
    logs: Array.from({ length: 20 }, (_, index) => log(20 - index, "retained")),
}
const searchPage = {
    id: "j1",
    workflow_id: "w1",
    logs: Array.from({ length: 40 }, (_, index) => log(40 - index, "hit")),
    highlight_token: "timeout",
}

const row = (lineNumber: number) => document.getElementById(`L${lineNumber}`) as HTMLElement
/** The row body button; the first button in a row is the line-options trigger. */
const rowButton = (lineNumber: number) => row(lineNumber).querySelector('button[aria-label="Select log entry"]') as HTMLButtonElement
const visibleLines = () =>
    [...document.querySelectorAll("[data-line-number]")].map((element) => Number(element.getAttribute("data-line-number")))
const selectionRangeCount = () => window.getSelection()?.rangeCount ?? 0
/** Selects a row's text, as dragging across the log would leave behind. */
const selectRowText = (lineNumber: number) => {
    const range = document.createRange()
    range.selectNodeContents(row(lineNumber))
    const selection = window.getSelection()!
    selection.removeAllRanges()
    selection.addRange(range)
}
/**
 * jsdom reports a listener exception as an `error` event on the window instead
 * of rethrowing it, so a handler that misbehaves is only visible if the test
 * collects those errors itself. Nothing else in this file raises one, so an empty
 * result means the handlers behaved.
 */
const windowErrorsWhile = async (dispatch: () => void) => {
    const errors: string[] = []
    const onError = (event: ErrorEvent) => { errors.push(event.message) }
    window.addEventListener("error", onError)
    try {
        await act(async () => {
            dispatch()
        })
    } finally {
        window.removeEventListener("error", onError)
    }
    return errors
}
const isSelected = (lineNumber: number) => row(lineNumber).getAttribute("data-selected") !== null
const fragment = () => window.location.hash
const clickLine = async (lineNumber: number, shiftKey = false) => {
    await act(async () => {
        fireEvent.click(rowButton(lineNumber), { shiftKey })
    })
}
/**
 * Waits for a real animation frame, then one macrotask. The re-arm queued by the
 * component is scheduled with `requestAnimationFrame` and jsdom runs every callback
 * registered for a frame in order, so waiting on a frame of our own is what makes
 * the component's callback count — a fixed sleep only loses that guarantee when the
 * runner is busy enough to push the frame past it.
 */
const flushFrame = async () => {
    await act(async () => {
        await new Promise<void>((resolve) => {
            requestAnimationFrame(() => { setTimeout(resolve, 0) })
        })
    })
}

function renderViewer(jobStatus = "COMPLETED") {
    const queryClient = new QueryClient({
        defaultOptions: { queries: { retry: false, gcTime: 0 }, mutations: { retry: false } },
    })

    // Subscribes the tree so the fake router can re-render after a push, the
    // way the Next.js router would after a real navigation.
    function Harness({ status }: { status: string }) {
        const [, setTick] = useState(0)
        useEffect(() => {
            mocks.notify = () => setTick((value) => value + 1)
            return () => {
                mocks.notify = null
            }
        }, [])
        return <LogsViewer workflowId="w1" jobId="j1" jobStatus={status} completedAt="2026-01-01T00:00:30Z" />
    }

    const view = render(
        <QueryClientProvider client={queryClient}>
            <Harness status={jobStatus} />
        </QueryClientProvider>,
    )
    return {
        ...view,
        setJobStatus: (next: string) => view.rerender(
            <QueryClientProvider client={queryClient}>
                <Harness status={next} />
            </QueryClientProvider>,
        ),
    }
}

const rangeChanged = async (startIndex: number, endIndex: number) => {
    await act(async () => {
        mocks.virtuosoProps?.rangeChanged({ startIndex, endIndex })
    })
}
const endReached = async () => {
    await act(async () => {
        mocks.virtuosoProps?.endReached()
    })
}

/** jsdom implements none of these; `installDomStubs` stands in for them. */
const pointerCaptureMembers = [
    "hasPointerCapture",
    "setPointerCapture",
    "releasePointerCapture",
    "scrollIntoView",
] as const

/** Captured before the first stub is installed, so `afterEach` can undo it. */
const originalPointerCaptureDescriptors = pointerCaptureMembers.map((member) =>
    Object.getOwnPropertyDescriptor(Element.prototype, member)
)

let clipboardDescriptor: PropertyDescriptor | undefined

beforeEach(() => {
    clipboardDescriptor = Object.getOwnPropertyDescriptor(navigator, "clipboard")
    window.history.replaceState(null, "", "/workflows/w1/jobs/j1")
    mocks.pathname = "/workflows/w1/jobs/j1"
    mocks.search = ""
    mocks.notify = null
    mocks.virtuosoProps = null
    mocks.scrollToIndex.mockReset()
    mocks.scrollIntoView.mockReset()
    mocks.scrollTo.mockReset()
    mocks.objectUrls.length = 0
    // jsdom implements neither object URLs nor anchor downloads.
    const UrlWithObjectUrls = class extends URL {}
    const urlStub = UrlWithObjectUrls as unknown as { createObjectURL: () => string, revokeObjectURL: (_url: string) => void }
    urlStub.createObjectURL = () => {
        const url = `blob:http://localhost/${mocks.objectUrls.length + 1}`
        mocks.objectUrls.push(url)
        return url
    }
    urlStub.revokeObjectURL = () => {}
    vi.stubGlobal("URL", UrlWithObjectUrls)
    mocks.anchorClick = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => {})
    const navigate = (url: string) => {
        const parsed = new URL(url, "http://localhost")
        mocks.pathname = parsed.pathname
        mocks.search = parsed.searchParams.toString()
        mocks.notify?.()
    }
    mocks.push.mockClear().mockImplementation(navigate)
    mocks.replace.mockClear().mockImplementation(navigate)
    mocks.fetchApi.mockReset()
    mocks.fetchApiJson.mockReset().mockImplementation(async (url: string) => {
        if (url.includes("/logs/search")) return searchPage
        if (url.includes("cursor=page-2")) return olderPage
        if (url.includes("/logs")) return newerPage
        if (url.includes("/workflows/w1")) return workflow
        throw new Error(`unexpected transport call: ${url}`)
    })
    mocks.toast.error.mockClear()
    mocks.toast.success.mockClear()
    mocks.toast.warning.mockClear()
    installDomStubs()
})

afterEach(() => {
    cleanup()
    // The debounce test below scopes fake timers to itself; this is the net for
    // a failure that leaves them installed, so no later test inherits a clock.
    vi.useRealTimers()
    vi.restoreAllMocks()
    vi.unstubAllGlobals()
    // Tests assign the hash directly, so clear it here too: a test that starts
    // by asserting "no selection" must not inherit the previous test's anchor.
    window.history.replaceState(null, "", "/workflows/w1/jobs/j1")
    // `userEvent.setup()` and the clipboard test below both replace the
    // descriptor; put the one this file started with back.
    if (clipboardDescriptor) {
        Object.defineProperty(navigator, "clipboard", clipboardDescriptor)
    } else {
        Reflect.deleteProperty(navigator, "clipboard")
    }
    restoreDomStubs()
})

const loadedLines = (count: number) =>
    waitFor(() => expect(visibleLines()).toHaveLength(count))

it("pages in older logs once per end-reached while a page request is in flight", async () => {
    let releasePage: (() => void) | null = null
    mocks.fetchApiJson.mockImplementation(async (url: string) => {
        if (url.includes("/logs/search")) return searchPage
        if (url.includes("cursor=page-2")) {
            await new Promise<void>((resolve) => {
                releasePage = resolve
            })
            return olderPage
        }
        if (url.includes("/logs")) return newerPage
        return workflow
    })

    renderViewer()
    await loadedLines(20)
    expect(visibleLines()).toEqual(Array.from({ length: 20 }, (_, index) => index + 1))

    // A page request is requested once, and further end-reached signals while it
    // is still in flight do not queue another one.
    await endReached()
    await waitFor(() => expect(pageRequests()).toHaveLength(1))
    await rangeChanged(15, 19)
    await endReached()
    await endReached()
    expect(pageRequests()).toHaveLength(1)

    await act(async () => {
        releasePage?.()
    })
    await loadedLines(40)

    // The exhausted cursor stops further paging.
    await endReached()
    await waitFor(() => expect(screen.getByText("retained 1")).toBeTruthy())
    expect(pageRequests()).toHaveLength(1)
})

it("follows a deep link past the loaded page, scrolls once, and corrects the rendered range", async () => {
    window.location.hash = "#L30"

    renderViewer()
    await loadedLines(20)
    await loadedLines(40)

    expect(pageRequests()).toHaveLength(1)
    await waitFor(() => expect(mocks.scrollToIndex).toHaveBeenCalledTimes(1))
    expect(mocks.scrollToIndex).toHaveBeenCalledWith({ index: 29, align: "start" })
    expect(screen.getByText("retained 11")).toBeTruthy()

    // A rendered range that excludes the target must not trigger a correction.
    await rangeChanged(0, 10)
    expect(mocks.scrollIntoView).not.toHaveBeenCalled()

    await rangeChanged(25, 39)
    await waitFor(() => expect(mocks.scrollIntoView).toHaveBeenCalledTimes(1))
    const [correction] = mocks.scrollIntoView.mock.calls[0] as unknown as [Record<string, unknown>]
    expect(correction.index).toBe(29)
    expect(correction.align).toBe("start")
    const calculateViewLocation = correction.calculateViewLocation as (_args: { locationParams: Record<string, unknown> }) => Record<string, unknown>
    expect(calculateViewLocation({ locationParams: { index: 29, offsetTop: 10 } })).toEqual({ index: 29, offsetTop: 10, align: "start" })

    // Completing the correction latches it: the same selection is not corrected
    // again by later range updates.
    const done = correction.done as () => void
    await act(async () => {
        done()
    })
    await rangeChanged(25, 39)
    await rangeChanged(29, 39)
    expect(mocks.scrollIntoView).toHaveBeenCalledTimes(1)
})

it("re-scrolls a preserved deep link when the dataset changes through history navigation", async () => {
    // A shareable link that carries both a filter and a selection: opening it
    // scrolls the selection inside the filtered dataset.
    mocks.search = "stream=stderr"
    window.location.hash = "#L30"

    renderViewer()
    await waitFor(() => expect(screen.getByText("hit 1")).toBeTruthy())
    await waitFor(() => expect(mocks.scrollToIndex).toHaveBeenCalledWith({ index: 29, align: "start" }))
    await rangeChanged(25, 39)
    await waitFor(() => expect(mocks.scrollIntoView).toHaveBeenCalledTimes(1))
    await act(async () => {
        (mocks.scrollIntoView.mock.calls[0][0] as { done: () => void }).done()
    })
    expect(pageRequests()).toHaveLength(0)

    // Back navigation drops the filter but keeps the selected line. The
    // selection must be re-established and scrolled in the new dataset instead
    // of being assumed already visible.
    await act(async () => {
        mocks.search = ""
        mocks.notify?.()
        window.dispatchEvent(new PopStateEvent("popstate"))
    })

    await waitFor(() => expect(screen.getByText("retained 1")).toBeTruthy())
    await loadedLines(40)
    await waitFor(() => expect(pageRequests()).toHaveLength(1))
    await waitFor(() => expect(mocks.scrollToIndex).toHaveBeenCalledTimes(2))
    expect(mocks.scrollToIndex).toHaveBeenLastCalledWith({ index: 29, align: "start" })

    // The rendered-range correction is re-armed for the new dataset too.
    await rangeChanged(25, 39)
    await waitFor(() => expect(mocks.scrollIntoView).toHaveBeenCalledTimes(2))
    expect((mocks.scrollIntoView.mock.calls[1][0] as { index: number }).index).toBe(29)
})

it.each([
    ["#L99", "Log line 99 is unavailable"],
    ["#L99-L120", "Some selected log lines are unavailable"],
    ["#L7", null],
])("warns once when a deep-linked selection cannot be resolved (%s)", async (hash, warning) => {
    mocks.fetchApiJson.mockImplementation(async (url: string) => {
        if (url.includes("/logs/search")) return searchPage
        if (url.includes("/logs")) return { ...newerPage, cursor: undefined }
        return workflow
    })
    window.location.hash = hash

    const view = renderViewer()
    await loadedLines(20)

    if (warning) {
        await waitFor(() => expect(mocks.toast.warning).toHaveBeenCalledTimes(1))
        expect(mocks.toast.warning).toHaveBeenCalledWith(warning)
        expect(pageRequests()).toHaveLength(0)
    } else {
        expect(mocks.toast.warning).not.toHaveBeenCalled()
        await waitFor(() => expect(mocks.scrollToIndex).toHaveBeenCalledWith({ index: 6, align: "start" }))
    }

    // Re-rendering the same view must not repeat the warning.
    view.setJobStatus("COMPLETED")
    await rangeChanged(0, 19)
    expect(mocks.toast.warning).toHaveBeenCalledTimes(warning ? 1 : 0)
})

it("does not repeat an unavailable warning when the address re-delivers the same line", async () => {
    mocks.fetchApiJson.mockImplementation(async (url: string) => {
        if (url.includes("/logs/search")) return searchPage
        if (url.includes("/logs")) return { ...newerPage, cursor: undefined }
        return workflow
    })
    window.location.hash = "#L99"

    renderViewer()
    await loadedLines(20)
    await waitFor(() => expect(mocks.toast.warning).toHaveBeenCalledTimes(1))

    // Following the same link again re-selects the same missing line, which the
    // reader has already been told about: the viewer must not nag twice.
    await act(async () => {
        setFragment("#L99")
    })
    expect(mocks.toast.warning).toHaveBeenCalledTimes(1)
    expect(mocks.toast.warning).toHaveBeenCalledWith("Log line 99 is unavailable")
})

it("keeps a resolved deep link in place when the address re-delivers it", async () => {
    window.location.hash = "#L30"

    renderViewer()
    await loadedLines(20)
    await loadedLines(40)
    await waitFor(() => expect(mocks.scrollToIndex).toHaveBeenCalledTimes(1))
    await rangeChanged(25, 39)
    await waitFor(() => expect(mocks.scrollIntoView).toHaveBeenCalledTimes(1))
    await act(async () => {
        (mocks.scrollIntoView.mock.calls[0][0] as { done: () => void }).done()
    })

    // Re-entering the same link finds the line already scrolled into place, so the
    // reader keeps their position instead of being yanked back. A re-arm is queued
    // in an animation frame, which jsdom does not flush inside `act`, so the test
    // waits one out before reading the counts: without the wait they would still
    // read 1 and 1 for the wrong reason.
    await act(async () => {
        setFragment("#L30")
    })
    await flushFrame()
    expect(mocks.scrollToIndex).toHaveBeenCalledTimes(1)
    expect(mocks.scrollIntoView).toHaveBeenCalledTimes(1)
})

it("drops a rendered-range correction whose selection was replaced first", async () => {
    window.location.hash = "#L30"

    renderViewer()
    await loadedLines(20)
    await loadedLines(40)
    await waitFor(() => expect(mocks.scrollToIndex).toHaveBeenCalledWith({ index: 29, align: "start" }))

    // A range change schedules a correction for line 30; moving the selection in
    // the same turn replaces it before the browser paints a frame.
    await act(async () => {
        mocks.virtuosoProps?.rangeChanged({ startIndex: 25, endIndex: 39 })
        setFragment("#L35")
    })

    // Only the surviving selection is corrected. Scrolling to the replaced line
    // would move the reader away from the line they just asked for.
    await waitFor(() => expect(mocks.scrollToIndex).toHaveBeenLastCalledWith({ index: 34, align: "start" }))
    await rangeChanged(34, 39)
    await waitFor(() => expect(mocks.scrollIntoView).toHaveBeenCalledTimes(1))
    expect((mocks.scrollIntoView.mock.calls[0][0] as { index: number }).index).toBe(34)
})

it("does not mark a replaced selection as scrolled when its correction lands late", async () => {
    window.location.hash = "#L30"

    renderViewer()
    await loadedLines(20)
    await loadedLines(40)
    await waitFor(() => expect(mocks.scrollToIndex).toHaveBeenCalledTimes(1))
    await rangeChanged(25, 39)
    await waitFor(() => expect(mocks.scrollIntoView).toHaveBeenCalledTimes(1))
    const staleDone = (mocks.scrollIntoView.mock.calls[0][0] as { done: () => void }).done

    // Move to another line and correct it, then let the first correction finish.
    await act(async () => {
        setFragment("#L35")
    })
    await rangeChanged(34, 39)
    await waitFor(() => expect(mocks.scrollIntoView).toHaveBeenCalledTimes(2))
    await act(async () => {
        staleDone()
    })

    // Line 30 was never confirmed as scrolled, so returning to it scrolls again
    // instead of trusting a correction that belonged to an abandoned selection.
    await act(async () => {
        setFragment("#L30")
    })
    await waitFor(() => expect(mocks.scrollToIndex).toHaveBeenCalledTimes(3))
    expect(mocks.scrollToIndex).toHaveBeenLastCalledWith({ index: 29, align: "start" })
})

it("tracks selection changes made through the address fragment", async () => {
    renderViewer()
    await loadedLines(20)

    await act(async () => {
        fireEvent.click(rowButton(3))
    })
    expect(fragment()).toBe("#L3")
    expect(isSelected(3)).toBe(true)
    expect(isSelected(4)).toBe(false)

    // Back/forward navigation re-syncs the selection from the fragment.
    await act(async () => {
        setFragment("#L5-L7")
    })
    expect([5, 6, 7].map(isSelected)).toEqual([true, true, true])
    expect(isSelected(3)).toBe(false)

    await act(async () => {
        setFragment("#L2")
    })
    expect(isSelected(2)).toBe(true)
    expect(isSelected(5)).toBe(false)

    // A fragment that is not a selection clears the highlight.
    await act(async () => {
        setFragment("")
    })
    expect(visibleLines().some(isSelected)).toBe(false)
})

it("selects and extends a range from row clicks while ignoring drags and line menus", async () => {
    const user = userEvent.setup()
    renderViewer()
    await loadedLines(20)

    await clickLine(2)
    expect(fragment()).toBe("#L2")

    await clickLine(4, true)
    expect(fragment()).toBe("#L2-L4")
    expect([2, 3, 4].map(isSelected)).toEqual([true, true, true])

    // The anchor stays on the first line, so a later shift-click widens the
    // same range instead of restarting it.
    await clickLine(6, true)
    expect(fragment()).toBe("#L2-L6")
    expect([2, 3, 4, 5, 6].map(isSelected)).toEqual([true, true, true, true, true])

    // A click that follows a drag is text selection, not a range change.
    const dragButton = rowButton(8)
    await act(async () => {
        fireEvent.pointerDown(dragButton, { clientX: 10, clientY: 10, button: 0 })
        fireEvent.pointerMove(dragButton, { clientX: 60, clientY: 40 })
        fireEvent.click(dragButton)
    })
    expect(fragment()).toBe("#L2-L6")

    // The per-line actions live in the gutter beside the row button, so
    // opening a row's menu is not a selection gesture.
    await user.click(row(9).querySelector('button[aria-label="Line options"]') as HTMLButtonElement)
    expect(await screen.findByRole("menu")).toBeTruthy()
    expect(screen.getByRole("menuitem", { name: /Copy line/ })).toBeTruthy()
    expect(fragment()).toBe("#L2-L6")
    expect([2, 3, 4, 5, 6].map(isSelected)).toEqual([true, true, true, true, true])

    await user.keyboard("{Escape}")
    await waitFor(() => expect(screen.queryByRole("menu")).toBeNull())

    await clickLine(11)
    expect(fragment()).toBe("#L11")
    expect([2, 3, 4, 5, 6].map(isSelected)).toEqual([false, false, false, false, false])
    expect(isSelected(11)).toBe(true)
})

it("tells a drag apart from a click and drops the text selection while extending", async () => {
    renderViewer()
    await loadedLines(20)
    const pressedLine = rowButton(5)

    // A press that never travels the drag threshold is still a click.
    await act(async () => {
        fireEvent.pointerDown(pressedLine, { clientX: 30, clientY: 30, button: 0 })
        fireEvent.pointerMove(pressedLine, { clientX: 32, clientY: 31 })
    })
    await clickLine(5)
    expect(fragment()).toBe("#L5")

    // Selecting a range by keyboard-and-pointer means shift-clicking, which must
    // not leave the browser's own text selection highlighting the row as well.
    selectRowText(4)
    expect(selectionRangeCount()).toBe(1)

    await act(async () => {
        // dispatchEvent reports false when a handler cancelled the press.
        expect(fireEvent.pointerDown(rowButton(7), { clientX: 30, clientY: 30, shiftKey: true, button: 0 })).toBe(false)
    })
    expect(selectionRangeCount()).toBe(0)

    await clickLine(7, true)
    expect(fragment()).toBe("#L5-L7")
    expect([5, 6, 7].map(isSelected)).toEqual([true, true, true])
})

it("keeps a selection through a pointer move that has no press behind it", async () => {
    renderViewer()
    await loadedLines(20)

    // A pointer travelling over the log without a button held is not a drag, so
    // it neither claims the selection nor records a gesture the next click
    // would mistake for one. With no press recorded there is no drag to measure,
    // and the handler must not reach for one.
    await clickLine(3)
    expect(fragment()).toBe("#L3")

    expect(await windowErrorsWhile(() => {
        fireEvent.pointerMove(rowButton(3), { clientX: 300, clientY: 40 })
        fireEvent.pointerMove(rowButton(7), { clientX: 300, clientY: 40 })
    })).toEqual([])
    expect(fragment()).toBe("#L3")
    expect(isSelected(3)).toBe(true)

    await clickLine(7)
    expect(fragment()).toBe("#L7")
    expect(isSelected(7)).toBe(true)
})

it("selects a line after a press that is not the primary button", async () => {
    renderViewer()
    await loadedLines(20)

    // Only a shift+primary press starts a range gesture, so this one has to leave
    // a text selection the reader made themselves alone.
    selectRowText(4)
    expect(selectionRangeCount()).toBe(1)

    await act(async () => {
        fireEvent.pointerDown(rowButton(3), { clientX: 12, clientY: 40, shiftKey: true, button: 1 })
    })
    expect(selectionRangeCount()).toBe(1)

    await clickLine(3)
    expect(fragment()).toBe("#L3")
    expect(isSelected(3)).toBe(true)
})

it("drops the selection and applies the debounced query when the search box changes", async () => {
    // Fake timers are scoped to this test: the 500ms settle is driven by the
    // clock instead of by how long the machine happened to take. Nothing else in
    // this file installs them, and `afterEach` restores the real clock.
    vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout"] })
    try {
        // Polling helpers cannot drive an installed clock, so the test moves it
        // itself and reads the DOM in between.
        const settle = async (ms = 0) => {
            await act(async () => {
                await vi.advanceTimersByTimeAsync(ms)
            })
        }
        const settleLines = async (count: number) => {
            const attempts = 20
            for (let attempt = 0; attempt < attempts && visibleLines().length !== count; attempt += 1) {
                await settle()
            }
            // The attempt count is in the message so a give-up reads as a settle
            // that ran out of turns, not as a viewer that rendered the wrong rows.
            expect(visibleLines(), `gave up after ${attempts} settle turns`).toHaveLength(count)
        }

        renderViewer()
        await settleLines(20)

        // An input that never changes must not re-query the viewer: the debounce
        // timer fires and finds the input already equal to the applied query.
        await settle(600)
        expect(mocks.push).not.toHaveBeenCalled()

        await act(async () => {
            fireEvent.click(rowButton(4))
        })
        expect(fragment()).toBe("#L4")

        const searchInput = screen.getByPlaceholderText("Search logs... (Ctrl+F)")
        await act(async () => {
            fireEvent.change(searchInput, { target: { value: "boom" } })
        })

        // Editing the query invalidates the line selection immediately.
        expect(fragment()).toBe("")
        expect(isSelected(4)).toBe(false)

        // The query itself waits for the input to settle, and then switches the
        // viewer to the search dataset.
        await settle(600)
        expect(mocks.push).toHaveBeenCalledWith("/workflows/w1/jobs/j1?q=boom")
        expect(mocks.push).toHaveBeenCalledTimes(1)
        await settleLines(40)

        // A settled query is not sent again just because time passed.
        await settle(600)
        expect(mocks.push).toHaveBeenCalledTimes(1)

        // Clearing the box returns the viewer to the unfiltered dataset.
        await act(async () => {
            fireEvent.change(searchInput, { target: { value: "" } })
        })
        await settle(600)
        expect(mocks.push).toHaveBeenLastCalledWith("/workflows/w1/jobs/j1")
        await settleLines(20)
    } finally {
        vi.useRealTimers()
    }
})

it("focuses the search box with the keyboard shortcut and releases it with escape", async () => {
    renderViewer()
    await loadedLines(20)
    const searchInput = screen.getByPlaceholderText("Search logs... (Ctrl+F)")
    expect(document.activeElement).not.toBe(searchInput)

    await act(async () => {
        window.dispatchEvent(new KeyboardEvent("keydown", { key: "f", ctrlKey: true, cancelable: true }))
    })
    expect(document.activeElement).toBe(searchInput)

    await act(async () => {
        window.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", cancelable: true }))
    })
    expect(document.activeElement).not.toBe(searchInput)
})

it("switches the dataset when the log level filter changes and drops the selection", async () => {
    mocks.fetchApiJson.mockImplementation(async (url: string) => {
        if (url.includes("stream=stderr")) {
            return {
                id: "j1",
                workflow_id: "w1",
                logs: [{ ...log(9, "stderr"), stream: "stderr" }, { ...log(8, "stderr"), stream: "stderr" }],
            }
        }
        if (url.includes("/logs/search")) return searchPage
        if (url.includes("/logs")) return newerPage
        return workflow
    })

    renderViewer()
    await loadedLines(20)
    await act(async () => {
        fireEvent.click(rowButton(2))
    })
    expect(fragment()).toBe("#L2")

    await act(async () => {
        fireEvent.click(screen.getByLabelText("Filter logs"))
    })
    await act(async () => {
        fireEvent.click(await screen.findByLabelText("stderr"))
    })

    await waitFor(() => expect(mocks.push).toHaveBeenCalledWith("/workflows/w1/jobs/j1?stream=stderr"))
    await waitFor(() => expect(visibleLines()).toHaveLength(2))
    expect(screen.getByText("stderr 9")).toBeTruthy()
    expect(fragment()).toBe("")
    expect(pageRequests()).toHaveLength(0)

    // Choosing "all" again drops the filter from the address.
    await act(async () => {
        fireEvent.click(screen.getByLabelText("Filter logs"))
    })
    await act(async () => {
        fireEvent.click(await screen.findByLabelText("All"))
    })
    await waitFor(() => expect(mocks.push).toHaveBeenCalledWith("/workflows/w1/jobs/j1"))
    await waitFor(() => expect(visibleLines()).toHaveLength(20))
})

it("downloads the retained logs from the download popover", async () => {
    mocks.fetchApi.mockImplementation(async () => ({ blob: async () => new Blob(["logs"]) }))
    renderViewer()
    await loadedLines(20)

    await act(async () => {
        fireEvent.click(screen.getByRole("button", { name: /Download/ }))
    })
    await act(async () => {
        fireEvent.change(await screen.findByLabelText("File name"), { target: { value: "run-42.txt" } })
    })
    const jsonlOption = await screen.findByLabelText("jsonl")
    await act(async () => {
        fireEvent.click(jsonlOption)
    })
    expect(jsonlOption.getAttribute("aria-checked")).toBe("true")

    await act(async () => {
        fireEvent.click(screen.getAllByRole("button", { name: /^Download$/ })[1])
    })

    await waitFor(() => expect(mocks.fetchApi).toHaveBeenCalledWith(
        "/workflows/w1/jobs/j1/logs/raw?format=jsonl",
        "failed to download logs",
    ))
    expect((mocks.anchorClick.mock.instances.at(-1) as HTMLAnchorElement).download).toBe("run-42.jsonl")
    expect(mocks.toast.success).toHaveBeenCalledWith("Logs downloaded successfully")
})

it("reports a download in flight in both the trigger and the action", async () => {
    let releaseDownload: (() => void) | null = null
    mocks.fetchApi.mockImplementation(() => new Promise((resolve) => {
        releaseDownload = () => resolve({ blob: async () => new Blob(["logs"]) })
    }))
    renderViewer()
    await loadedLines(20)

    await act(async () => {
        fireEvent.click(screen.getByRole("button", { name: /Download/ }))
    })
    await act(async () => {
        fireEvent.change(await screen.findByLabelText("File name"), { target: { value: "slow.txt" } })
    })
    await act(async () => {
        fireEvent.click(screen.getAllByRole("button", { name: /^Download$/ })[1])
    })

    // While the request is outstanding, both download buttons show the wait and
    // refuse a second click, so a slow download cannot be queued twice.
    const pending = screen.getAllByRole("button", { name: /^Download$/ }) as HTMLButtonElement[]
    await waitFor(() => expect(pending.every((button) => button.disabled)).toBe(true))
    // The spin utility comes from this view's own markup, so this does not depend
    // on which icon library renders the spinner.
    expect(pending.map((button) => Boolean(button.querySelector(".animate-spin")))).toEqual([true, true])
    expect(screen.getByLabelText("File name")).toHaveProperty("disabled", true)
    expect(mocks.fetchApi).toHaveBeenCalledTimes(1)

    // Finishing the download closes the popover and reports success.
    await act(async () => {
        releaseDownload?.()
    })
    await waitFor(() => expect(mocks.toast.success).toHaveBeenCalledWith("Logs downloaded successfully"))
    await waitFor(() => expect(screen.queryByLabelText("File name")).toBeNull())
    expect((mocks.anchorClick.mock.instances.at(-1) as HTMLAnchorElement).download).toBe("slow.txt")
})

it("keeps json rendering available only while the job has logs", async () => {
    mocks.fetchApiJson.mockImplementation(async (url: string) => {
        if (url.includes("/logs/search")) return searchPage
        if (url.includes("/logs")) return { ...newerPage, logs: [], cursor: undefined }
        return workflow
    })

    renderViewer("CANCELED")
    await waitFor(() => expect(screen.getByText("This job did not produce any logs")).toBeTruthy())
    expect(screen.getByRole("switch")).toHaveProperty("disabled", true)
})

it("reports a failed clipboard write without changing the selection", async () => {
    const user = userEvent.setup()
    Object.defineProperty(navigator, "clipboard", {
        configurable: true,
        value: { writeText: async () => { throw new Error("clipboard blocked") } },
    })

    renderViewer()
    await loadedLines(20)
    await act(async () => {
        fireEvent.click(rowButton(3))
    })
    expect(fragment()).toBe("#L3")

    await user.click(row(3).querySelector('button[aria-label="Line options"]') as HTMLButtonElement)
    await user.click(await screen.findByRole("menuitem", { name: /Copy line/ }))

    await waitFor(() => expect(mocks.toast.error).toHaveBeenCalledWith("Failed to copy log lines"))
    expect(fragment()).toBe("#L3")
    expect(isSelected(3)).toBe(true)
})

it("switches between raw and parsed JSON rendering through the address parameter", async () => {
    mocks.fetchApiJson.mockImplementation(async (url: string) => {
        if (url.includes("/logs/search")) return searchPage
        if (url.includes("/logs")) {
            return {
                id: "j1",
                workflow_id: "w1",
                logs: [
                    { ...log(2, "raw"), message: '{"level":"info","count":2}' },
                    { ...log(1, "raw"), message: "plain text" },
                ],
            }
        }
        return workflow
    })

    renderViewer()
    await loadedLines(2)
    expect(row(1).textContent).toBe('{"level":"info","count":2}')

    const jsonSwitch = screen.getByRole("switch")
    await act(async () => {
        fireEvent.click(jsonSwitch)
    })

    expect(mocks.replace).toHaveBeenCalledWith("/workflows/w1/jobs/j1?json=true", { scroll: false })
    await waitFor(() => expect(row(1).textContent).toContain('"level": "info"'))
    expect(row(2).textContent).toBe("plain text")
})

it("keeps the selected line in the address when parsed JSON rendering is switched off", async () => {
    mocks.fetchApiJson.mockImplementation(async (url: string) => {
        if (url.includes("/logs/search")) return searchPage
        if (url.includes("/logs")) {
            return {
                id: "j1",
                workflow_id: "w1",
                logs: [
                    { ...log(2, "raw"), message: '{"level":"info","count":2}' },
                    { ...log(1, "raw"), message: "plain text" },
                ],
            }
        }
        return workflow
    })
    mocks.search = "json=true"
    window.location.hash = "#L2"

    renderViewer()
    await loadedLines(2)
    expect(screen.getByRole("switch").getAttribute("aria-checked")).toBe("true")
    expect(row(1).textContent).toContain('"level": "info"')

    await act(async () => {
        fireEvent.click(screen.getByRole("switch"))
    })

    // Switching off drops only the parse flag: the selection fragment survives,
    // because it is part of the same shareable address.
    expect(mocks.replace).toHaveBeenCalledWith("/workflows/w1/jobs/j1#L2", { scroll: false })
    expect(mocks.search).toBe("")
    await waitFor(() => expect(row(1).textContent).toBe('{"level":"info","count":2}'))
    expect(isSelected(2)).toBe(true)
    expect(fragment()).toBe("#L2")
})

it("copies the selected lines and the shareable permalink from the line menu", async () => {
    // userEvent.setup() also installs the async clipboard stub jsdom lacks.
    const user = userEvent.setup()
    renderViewer()
    await loadedLines(20)

    const openLineMenu = async (lineNumber: number) => {
        await user.click(row(lineNumber).querySelector('button[aria-label="Line options"]') as HTMLButtonElement)
        return screen.findByRole("menu")
    }
    const copiedText = () => navigator.clipboard.readText()

    await clickLine(2)
    await clickLine(4, true)
    expect(fragment()).toBe("#L2-L4")

    // The line menu of the top selected line acts on the whole range.
    await openLineMenu(2)
    await user.click(await screen.findByRole("menuitem", { name: /Copy lines/ }))
    await waitFor(async () => expect(await copiedText()).toBe("retained 39\nretained 38\nretained 37"))
    expect(mocks.toast.success).toHaveBeenCalledWith("Log lines copied")

    await openLineMenu(2)
    await user.click(await screen.findByRole("menuitem", { name: /Copy permalink/ }))
    await waitFor(async () => expect(await copiedText()).toBe(`${window.location.origin}/workflows/w1/jobs/j1#L2-L4`))
    expect(mocks.toast.success).toHaveBeenCalledWith("Log permalink copied")
})

it("copies one unselected line and its own permalink", async () => {
    const user = userEvent.setup()
    renderViewer()
    await loadedLines(20)
    const copiedText = () => navigator.clipboard.readText()

    // With no range selected, the line menu of a row acts on that row alone.
    await user.click(row(6).querySelector('button[aria-label="Line options"]') as HTMLButtonElement)
    await user.click(await screen.findByRole("menuitem", { name: /Copy line/ }))

    await waitFor(async () => expect(await copiedText()).toBe("retained 35"))
    expect(mocks.toast.success).toHaveBeenCalledWith("Log line copied")
    expect(fragment()).toBe("")

    // The permalink of a single line also selects that line in the address.
    await user.click(row(6).querySelector('button[aria-label="Line options"]') as HTMLButtonElement)
    await user.click(await screen.findByRole("menuitem", { name: /Copy permalink/ }))

    await waitFor(async () => expect(await copiedText()).toBe(`${window.location.origin}/workflows/w1/jobs/j1#L6`))
    expect(mocks.toast.success).toHaveBeenCalledWith("Log permalink copied")
    expect(fragment()).toBe("#L6")
    expect(isSelected(6)).toBe(true)
})

it("reports a refused permalink copy and keeps the line it linked to", async () => {
    const user = userEvent.setup()
    Object.defineProperty(navigator, "clipboard", {
        configurable: true,
        value: { writeText: async () => { throw new Error("clipboard blocked") } },
    })

    renderViewer()
    await loadedLines(20)
    expect(fragment()).toBe("")

    await user.click(row(8).querySelector('button[aria-label="Line options"]') as HTMLButtonElement)
    await user.click(await screen.findByRole("menuitem", { name: /Copy permalink/ }))

    await waitFor(() => expect(mocks.toast.error).toHaveBeenCalledWith("Failed to copy log permalink"))
    expect(mocks.toast.success).not.toHaveBeenCalled()
    // Only the clipboard refused: the line the permalink pointed at stays
    // selected, so the user can still see and share it by hand.
    expect(fragment()).toBe("#L8")
    expect(isSelected(8)).toBe(true)
})

it("requests at most one page while a deep-linked page is still in flight", async () => {
    let releasePage: (() => void) | null = null
    mocks.fetchApiJson.mockImplementation(async (url: string) => {
        if (url.includes("cursor=page-2")) {
            await new Promise<void>((resolve) => {
                releasePage = resolve
            })
            return olderPage
        }
        if (url.includes("/logs/search")) return searchPage
        if (url.includes("/logs")) return newerPage
        return workflow
    })
    window.location.hash = "#L30"

    renderViewer()
    await loadedLines(20)
    await waitFor(() => expect(pageRequests()).toHaveLength(1))

    // A second deep link arriving while that page request is still outstanding
    // re-runs the selection effect with a new selection, and must not ask for a
    // second page.
    await act(async () => {
        setFragment("#L35")
    })
    expect(fragment()).toBe("#L35")
    expect(pageRequests()).toHaveLength(1)

    await act(async () => {
        releasePage?.()
    })
    await loadedLines(40)
    await waitFor(() => expect(mocks.scrollToIndex).toHaveBeenCalledWith({ index: 34, align: "start" }))
    expect(pageRequests()).toHaveLength(1)
})

const pageRequests = () =>
    mocks.fetchApiJson.mock.calls.map(([url]) => url as string).filter((url) => url.includes("cursor="))

function setFragment(hash: string) {
    window.history.replaceState(window.history.state, "", `${window.location.pathname}${window.location.search}${hash}`)
    window.dispatchEvent(new HashChangeEvent("hashchange"))
}

function installDomStubs() {
    if (!("ResizeObserver" in globalThis)) {
        class ResizeObserverStub {
            observe() {}
            unobserve() {}
            disconnect() {}
        }
        vi.stubGlobal("ResizeObserver", ResizeObserverStub)
    }
    if (!Element.prototype.hasPointerCapture) {
        Element.prototype.hasPointerCapture = () => false
        Element.prototype.setPointerCapture = () => {}
        Element.prototype.releasePointerCapture = () => {}
        Element.prototype.scrollIntoView = () => {}
    }
    if (!window.matchMedia) {
        vi.stubGlobal("matchMedia", (query: string) => ({
            matches: false,
            media: query,
            onchange: null,
            addEventListener: () => {},
            removeEventListener: () => {},
            addListener: () => {},
            removeListener: () => {},
            dispatchEvent: () => false,
        }))
    }
}

/** Puts the shared prototype back the way this file found it. */
function restoreDomStubs() {
    pointerCaptureMembers.forEach((member, index) => {
        const original = originalPointerCaptureDescriptors[index]
        if (original) {
            Object.defineProperty(Element.prototype, member, original)
        } else {
            Reflect.deleteProperty(Element.prototype, member)
        }
    })
}
