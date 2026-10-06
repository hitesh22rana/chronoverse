// @vitest-environment jsdom
/**
 * Mounted tests for the workflow list controls: how the search box, the filter
 * popover, the interval bounds, refresh and paging turn user gestures into
 * navigation and list requests, and how each of them behaves while a request is
 * still in flight.
 *
 * Only boundaries are mocked: the transport, the navigation hooks (with a
 * store-backed search string, so `router.push` re-renders the list the way a
 * real navigation hands the page new search params) and `sonner`. The Radix
 * popover/select, react-query and the interval helpers all run for real.
 */
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import {
    act,
    cleanup,
    fireEvent,
    render,
    screen,
    waitFor,
    within,
} from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { useSyncExternalStore } from "react"
import { afterEach, beforeEach, expect, it, vi } from "vitest"

import type { Workflow, WorkflowsResponse } from "./types"
import { Workflows } from "./workflows"

const mocks = vi.hoisted(() => {
    const searchStore = {
        value: "",
        listeners: new Set<() => void>(),
        get: () => searchStore.value,
        subscribe: (listener: () => void) => {
            searchStore.listeners.add(listener)
            return () => {
                searchStore.listeners.delete(listener)
            }
        },
        set: (next: string) => {
            searchStore.value = next
            searchStore.listeners.forEach((listener) => listener())
        },
    }

    return {
        searchStore,
        pathname: "/",
        push: vi.fn(),
        back: vi.fn(),
        replace: vi.fn(),
        fetchApi: vi.fn(),
        fetchApiJson: vi.fn(),
        toast: { error: vi.fn(), success: vi.fn(), warning: vi.fn() },
        list: null as unknown,
        listError: null as null | Error,
        holdList: false,
        releaseList: null as null | (() => void),
    }
})

vi.mock("next/navigation", () => ({
    usePathname: () => mocks.pathname,
    useRouter: () => ({ push: mocks.push, back: mocks.back, replace: mocks.replace }),
    useSearchParams: () => {
        // Subscribing here is what makes `router.push` re-render the list, the
        // same way a real navigation hands the page new search params.
        useSyncExternalStore(mocks.searchStore.subscribe, mocks.searchStore.get, mocks.searchStore.get)
        return new URLSearchParams(mocks.searchStore.value)
    },
}))

vi.mock("sonner", () => ({ toast: mocks.toast, Toaster: () => null }))

vi.mock("@/lib/api/client", () => ({
    fetchApi: mocks.fetchApi,
    fetchApiJson: mocks.fetchApiJson,
    createIdempotencyKey: () => "idempotency-key",
}))

const workflow = (overrides: Partial<Workflow> = {}): Workflow => ({
    id: "w1",
    name: "Nightly container",
    kind: "CONTAINER",
    payload: JSON.stringify({ image: "alpine:latest" }),
    build_status: "COMPLETED",
    interval: 15,
    max_consecutive_job_failures_allowed: 3,
    log_retention: true,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-02T00:00:00Z",
    ...overrides,
})

const heartbeat = workflow({ id: "w2", name: "API heartbeat", kind: "HEARTBEAT" })

function renderList(search = "") {
    mocks.searchStore.set(search)
    const queryClient = new QueryClient({
        defaultOptions: { queries: { retry: false, gcTime: 0 }, mutations: { retry: false } },
    })
    const user = userEvent.setup()
    const view = render(
        <QueryClientProvider client={queryClient}>
            <Workflows />
        </QueryClientProvider>,
    )
    return { ...view, user, queryClient }
}

const listRequests = () => mocks.fetchApiJson.mock.calls.map((call) => String(call[0]))
const lastListRequest = () => listRequests().at(-1)
const lastPush = () => mocks.push.mock.calls.at(-1)?.[0] as string

const searchBox = () => screen.getByPlaceholderText("Search workflows...(Ctrl+F)") as HTMLInputElement
const clearSearch = () => screen.queryByRole("button", { name: "Clear search and filters" })
/** Exact-name lookup would miss the active-filter badge inside the trigger. */
const filterButton = () => screen.getByRole("button", { name: /^Filters/ })
const refreshButton = () => screen.getByRole("button", { name: "Refresh" })
const nextPageButton = () => screen.getByRole("button", { name: "Next page" })
const previousPageButton = () => screen.getByRole("button", { name: "Previous page" })
const applyButton = () => screen.getByRole("button", { name: "Apply Filters" })
const minInput = () => screen.getByPlaceholderText("Min") as HTMLInputElement
const maxInput = () => screen.getByPlaceholderText("Max") as HTMLInputElement
const statusSelect = () => screen.getAllByRole("combobox")[0]
const kindSelect = () => screen.getAllByRole("combobox")[1]

/** The search box debounces for 500ms, so the wait has to outlast it. */
const afterDebounce = { timeout: 5000 } as const

async function openFilters(user: ReturnType<typeof userEvent.setup>) {
    await user.click(filterButton())
    return screen.getByText("Filter by")
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

beforeEach(() => {
    mocks.pathname = "/"
    mocks.list = { workflows: [workflow(), heartbeat] } as WorkflowsResponse
    mocks.listError = null
    mocks.holdList = false
    mocks.releaseList = null
    mocks.searchStore.set("")

    mocks.push.mockClear()
    mocks.back.mockClear()
    mocks.replace.mockClear()
    mocks.toast.error.mockClear()
    mocks.toast.success.mockClear()
    mocks.toast.warning.mockClear()

    // `router.push` writes the address, which the mocked search params re-read.
    mocks.push.mockImplementation((url: string) => {
        mocks.searchStore.set(url.replace(/^[?]/, ""))
    })
    mocks.fetchApiJson.mockReset().mockImplementation(async () => {
        if (mocks.holdList) {
            await new Promise<void>((resolve) => {
                mocks.releaseList = resolve
            })
        }
        if (mocks.listError) throw mocks.listError
        return mocks.list
    })
    installDomStubs()
})

afterEach(() => {
    cleanup()
    vi.unstubAllGlobals()
    restoreDomStubs()
})

it("lists what the unfiltered request returned", async () => {
    renderList()

    expect(await screen.findByText("Nightly container")).toBeTruthy()
    expect(screen.getByText("API heartbeat")).toBeTruthy()
    expect(lastListRequest()).toBe("/workflows")
    expect(searchBox()).toHaveProperty("value", "")
    // Nothing is being searched or filtered, so there is nothing to clear.
    expect(clearSearch()).toBeNull()
    expect(refreshButton()).toHaveProperty("disabled", false)
    expect(previousPageButton()).toHaveProperty("disabled", true)
    expect(nextPageButton()).toHaveProperty("disabled", true)
})

it("holds refresh back while the first list request is in flight", async () => {
    mocks.holdList = true
    renderList()

    await waitFor(() => expect(refreshButton()).toHaveProperty("disabled", true))
    expect(refreshButton().querySelector("svg")?.getAttribute("class")).toContain("animate-spin")
    // A skeleton stands in for the rows, but every control is already usable.
    expect(document.querySelectorAll("[data-slot=skeleton]").length).toBeGreaterThan(0)
    expect(screen.queryByText("Nightly container")).toBeNull()

    await act(async () => {
        mocks.releaseList?.()
    })

    expect(await screen.findByText("Nightly container")).toBeTruthy()
    await waitFor(() => expect(refreshButton()).toHaveProperty("disabled", false))
    expect(refreshButton().querySelector("svg")?.getAttribute("class")).not.toContain("animate-spin")
})

it("waits for the search debounce before asking for the typed query", async () => {
    const { user } = renderList()
    await screen.findByText("Nightly container")
    const requestsBeforeTyping = listRequests().length

    await user.type(searchBox(), "nightly")

    // The text is local until the debounce elapses, so nothing is requested.
    expect(searchBox()).toHaveProperty("value", "nightly")
    expect(mocks.push).not.toHaveBeenCalled()
    expect(clearSearch()).toBeTruthy()

    await waitFor(() => expect(lastPush()).toBe("?query=nightly"), afterDebounce)

    await waitFor(() => expect(lastListRequest()).toBe("/workflows?query=nightly"))
    expect(listRequests().length).toBeGreaterThan(requestsBeforeTyping)
})

it("does not re-request when the debounce fires without a change", async () => {
    renderList("query=nightly")
    await waitFor(() => expect(searchBox()).toHaveProperty("value", "nightly"))
    const requestsBefore = listRequests().length

    // Two full debounce periods: the input already matches the applied query.
    await act(async () => {
        await new Promise((resolve) => setTimeout(resolve, 1200))
    })

    expect(mocks.push).not.toHaveBeenCalled()
    expect(listRequests()).toHaveLength(requestsBefore)
})

it("clears the search text and the applied query from the search box", async () => {
    const { user } = renderList("query=nightly&status=FAILED")
    await waitFor(() => expect(lastListRequest()).toBe("/workflows?query=nightly&build_status=FAILED"))
    expect(searchBox()).toHaveProperty("value", "nightly")
    expect(clearSearch()).toBeTruthy()

    await user.click(clearSearch()!)

    // The clear affordance exists because a query is applied; clearing drops the
    // query and leaves the status filter alone.
    await waitFor(() => expect(lastPush()).toBe("?status=FAILED"))
    await waitFor(() => expect(lastListRequest()).toBe("/workflows?build_status=FAILED"))
    expect(searchBox()).toHaveProperty("value", "")
    // A filter is still applied, so the affordance stays.
    expect(clearSearch()).toBeTruthy()
})

it("keeps the clear affordance for an active filter with no search text", async () => {
    const { user } = renderList("status=FAILED")
    await waitFor(() => expect(within(filterButton()).getByText("1")).toBeTruthy())
    expect(searchBox()).toHaveProperty("value", "")

    await user.click(clearSearch()!)

    // There is no query to drop, so only the search term leaves the address and
    // the applied status filter survives.
    await waitFor(() => expect(lastPush()).toBe("?status=FAILED"))
    await waitFor(() => expect(lastListRequest()).toBe("/workflows?build_status=FAILED"))
    expect(searchBox()).toHaveProperty("value", "")
})

it("keeps a pending filter out of the request until Apply", async () => {
    const { user } = renderList()
    await screen.findByText("Nightly container")

    await openFilters(user)
    expect(screen.queryByRole("button", { name: /Clear all/ })).toBeNull()
    expect(within(filterButton()).queryByText("1")).toBeNull()
    // A reopened popover starts from the applied filters, which are none here.
    expect(statusSelect().textContent).toBe("All statuses")
    expect(kindSelect().textContent).toBe("All kinds")

    await user.click(statusSelect())
    await user.click(await screen.findByRole("option", { name: "Failed" }))
    await user.click(kindSelect())
    await user.click(await screen.findByRole("option", { name: "Container" }))

    // Picking values changes nothing about the request the list has issued.
    expect(mocks.push).not.toHaveBeenCalled()
    expect(lastListRequest()).toBe("/workflows")

    await user.click(applyButton())

    await waitFor(() => expect(lastPush()).toBe("?status=FAILED&kind=CONTAINER"))
    await waitFor(() => expect(lastListRequest()).toBe("/workflows?build_status=FAILED&kind=CONTAINER"))
    // Applying closes the popover, so the picks are no longer on screen.
    await waitFor(() => expect(screen.queryByText("Filter by")).toBeNull())
    expect(within(filterButton()).getByText("2")).toBeTruthy()
})

it("resends the list unfiltered when the selects go back to their unset choices", async () => {
    const { user } = renderList("status=FAILED&kind=CONTAINER")
    await waitFor(() => expect(lastListRequest()).toBe("/workflows?build_status=FAILED&kind=CONTAINER"))

    await openFilters(user)
    await user.click(kindSelect())
    await user.click(await screen.findByRole("option", { name: "All kinds" }))
    await user.click(applyButton())

    await waitFor(() => expect(lastPush()).toBe("?status=FAILED"))
    await waitFor(() => expect(lastListRequest()).toBe("/workflows?build_status=FAILED"))
    expect(within(filterButton()).getByText("1")).toBeTruthy()

    await openFilters(user)
    await user.click(statusSelect())
    await user.click(await screen.findByRole("option", { name: "All statuses" }))
    await user.click(applyButton())

    await waitFor(() => expect(lastPush()).toBe("?"))
    await waitFor(() => expect(lastListRequest()).toBe("/workflows"))
    expect(within(filterButton()).queryByText("1")).toBeNull()
})

it("reseeds the filter popover from the applied filters and drops abandoned edits", async () => {
    const { user } = renderList("status=FAILED&kind=HEARTBEAT")
    await waitFor(() => expect(within(filterButton()).getByText("2")).toBeTruthy())

    await openFilters(user)
    expect(statusSelect().textContent).toBe("Failed")
    expect(kindSelect().textContent).toBe("Heartbeat")

    // Reopening after abandoning an edit must not resurrect the abandoned pick.
    await user.click(statusSelect())
    await user.click(await screen.findByRole("option", { name: "Queued" }))
    await user.keyboard("{Escape}")
    await openFilters(user)

    expect(statusSelect().textContent).toBe("Failed")
    expect(kindSelect().textContent).toBe("Heartbeat")
    expect(mocks.push).not.toHaveBeenCalled()
})

it("refuses to apply an interval range whose maximum is below its minimum", async () => {
    const { user } = renderList()
    await screen.findByText("Nightly container")

    await openFilters(user)
    fireEvent.change(minInput(), { target: { value: "60" } })
    fireEvent.change(maxInput(), { target: { value: "5" } })

    const rangeError = await screen.findByRole("alert")
    expect(rangeError.textContent).toBe("Maximum must be greater than or equal to minimum.")
    expect(minInput().getAttribute("aria-invalid")).toBe("true")
    expect(maxInput().getAttribute("aria-invalid")).toBe("true")
    expect(minInput().getAttribute("aria-describedby")).toBe("workflow-interval-error")
    expect(maxInput().getAttribute("aria-describedby")).toBe("workflow-interval-error")
    expect(applyButton()).toHaveProperty("disabled", true)

    await user.click(applyButton())
    expect(mocks.push).not.toHaveBeenCalled()

    // A minimum on its own is a bound, not a range, so it applies right away.
    fireEvent.change(minInput(), { target: { value: "5" } })
    await waitFor(() => expect(screen.queryByRole("alert")).toBeNull())
    expect(minInput().getAttribute("aria-invalid")).toBe("false")
    expect(maxInput().getAttribute("aria-invalid")).toBe("false")
    expect(minInput().getAttribute("aria-describedby")).toBeNull()
    expect(maxInput().getAttribute("aria-describedby")).toBeNull()
    expect(applyButton()).toHaveProperty("disabled", false)
})

it("accepts a closed interval range and drops bounds outside the allowed minutes", async () => {
    const { user } = renderList()
    await screen.findByText("Nightly container")

    await openFilters(user)
    fireEvent.change(minInput(), { target: { value: "5" } })
    fireEvent.change(maxInput(), { target: { value: "5" } })
    expect(screen.queryByRole("alert")).toBeNull()
    expect(applyButton()).toHaveProperty("disabled", false)

    // Zero, 10081 and a non-numeric bound are all unusable, so each field keeps
    // the last value it could accept instead of forwarding it.
    fireEvent.change(minInput(), { target: { value: "0" } })
    fireEvent.change(maxInput(), { target: { value: "10081" } })
    expect(minInput()).toHaveProperty("value", "5")
    expect(maxInput()).toHaveProperty("value", "5")

    // An emptied bound is a removal, which is applied.
    fireEvent.change(maxInput(), { target: { value: "" } })
    expect(maxInput()).toHaveProperty("value", "")

    await user.click(applyButton())

    await waitFor(() => expect(lastPush()).toBe("?interval_min=5"))
    await waitFor(() => expect(lastListRequest()).toBe("/workflows?interval_min=5"))
    expect(within(filterButton()).getByText("1")).toBeTruthy()
})

it("blocks the keys that cannot type an interval bound", async () => {
    const { user } = renderList()
    await screen.findByText("Nightly container")
    await openFilters(user)
    expect(minInput()).toHaveProperty("value", "")

    // A number input answers "e" with scientific notation, so it is refused.
    expect(fireEvent.keyDown(minInput(), { key: "e" })).toBe(false)
    expect(fireEvent.keyDown(minInput(), { key: "-" })).toBe(false)
    expect(fireEvent.keyDown(maxInput(), { key: "." })).toBe(false)
    // A digit is left to the input on either bound.
    expect(fireEvent.keyDown(minInput(), { key: "5" })).toBe(true)
    expect(fireEvent.keyDown(maxInput(), { key: "5" })).toBe(true)

    await user.type(minInput(), "5")
    expect(minInput()).toHaveProperty("value", "5")
})

it("applies a rebuilt interval range over the previous one", async () => {
    const { user } = renderList("interval_min=5&interval_max=60")
    await waitFor(() => expect(lastListRequest()).toBe("/workflows?interval_min=5&interval_max=60"))
    expect(within(filterButton()).getByText("2")).toBeTruthy()

    await openFilters(user)
    expect(minInput()).toHaveProperty("value", "5")
    expect(maxInput()).toHaveProperty("value", "60")

    fireEvent.change(minInput(), { target: { value: "15" } })
    fireEvent.change(maxInput(), { target: { value: "" } })
    await user.click(applyButton())

    await waitFor(() => expect(lastPush()).toBe("?interval_min=15"))
    await waitFor(() => expect(lastListRequest()).toBe("/workflows?interval_min=15"))
    expect(within(filterButton()).getByText("1")).toBeTruthy()
})

it("clears every applied filter but keeps the search text", async () => {
    const { user } = renderList("query=nightly&status=FAILED&kind=CONTAINER&interval_min=5")
    await waitFor(() => expect(within(filterButton()).getByText("3")).toBeTruthy())

    await openFilters(user)
    expect(minInput()).toHaveProperty("value", "5")
    expect(maxInput()).toHaveProperty("value", "")

    await user.click(screen.getByRole("button", { name: /Clear all/ }))

    await waitFor(() => expect(lastPush()).toBe("?query=nightly"))
    await waitFor(() => expect(lastListRequest()).toBe("/workflows?query=nightly"))
    // Clearing drops the pending inputs too and closes the popover.
    await waitFor(() => expect(screen.queryByText("Filter by")).toBeNull())
    expect(within(filterButton()).queryByText("1")).toBeNull()
    expect(clearSearch()).toBeTruthy()

    // A fresh popover starts empty rather than showing the abandoned bounds.
    await openFilters(user)
    expect(minInput()).toHaveProperty("value", "")
})

it("re-requests the list on demand and keeps refresh disabled while one is in flight", async () => {
    const { user } = renderList()
    await screen.findByText("Nightly container")
    await waitFor(() => expect(refreshButton()).toHaveProperty("disabled", false))
    const requestsBefore = listRequests().length

    mocks.holdList = true
    await user.click(refreshButton())

    await waitFor(() => expect(refreshButton()).toHaveProperty("disabled", true))
    expect(refreshButton().querySelector("svg")?.getAttribute("class")).toContain("animate-spin")
    // A disabled control cannot queue a second request behind the pending one.
    expect(refreshButton()).toHaveProperty("disabled", true)
    expect(listRequests()).toHaveLength(requestsBefore + 1)

    await act(async () => {
        mocks.releaseList?.()
    })

    await waitFor(() => expect(refreshButton()).toHaveProperty("disabled", false))
})

it("walks the cursor forward and back", async () => {
    mocks.list = { workflows: [workflow()], cursor: "cursor-2" }
    const { user } = renderList()
    await screen.findByText("Nightly container")
    expect(nextPageButton()).toHaveProperty("disabled", false)

    await user.click(nextPageButton())

    await waitFor(() => expect(lastPush()).toBe("?cursor=cursor-2"))
    await waitFor(() => expect(lastListRequest()).toBe("/workflows?cursor=cursor-2"))
    await waitFor(() => expect(previousPageButton()).toHaveProperty("disabled", false))

    await user.click(previousPageButton())

    expect(mocks.back).toHaveBeenCalledTimes(1)
})

it("returns to the first page when a paginated list is searched", async () => {
    mocks.list = { workflows: [workflow()], cursor: "cursor-3" }
    const { user } = renderList("cursor=cursor-2")
    await waitFor(() => expect(lastListRequest()).toBe("/workflows?cursor=cursor-2"))
    expect(previousPageButton()).toHaveProperty("disabled", false)

    await user.type(searchBox(), "nightly")

    await waitFor(() => expect(lastPush()).toBe("?query=nightly"), afterDebounce)
    await waitFor(() => expect(lastListRequest()).toBe("/workflows?query=nightly"))
})

it("tells an emptied filtered list apart from one that never had workflows", async () => {
    mocks.list = { workflows: [] }
    renderList("status=FAILED")

    expect(await screen.findByText("No workflows found")).toBeTruthy()
    expect(screen.getByText("Try adjusting your search query or filters")).toBeTruthy()
    // The controls stay usable so the user can undo the filter that emptied it.
    expect(refreshButton()).toBeTruthy()
    expect(within(filterButton()).getByText("1")).toBeTruthy()

    cleanup()
    mocks.list = { workflows: [] }
    renderList()

    expect(await screen.findByText("No workflows found")).toBeTruthy()
    expect(screen.getByText("Create your first workflow to get started")).toBeTruthy()
})

it("reports a failed list load without losing the controls", async () => {
    mocks.listError = new Error("failed to fetch workflows: offline")
    renderList()

    await waitFor(() =>
        expect(mocks.toast.error).toHaveBeenCalledWith("failed to fetch workflows: offline"),
    )
    expect(await screen.findByText("No workflows found")).toBeTruthy()
    expect(refreshButton()).toBeTruthy()
    expect(filterButton()).toBeTruthy()
    expect(searchBox()).toHaveProperty("value", "")
})

it("takes focus with the find shortcut and gives it up on escape", async () => {
    renderList()
    await screen.findByText("Nightly container")
    expect(document.activeElement).toBe(document.body)

    // The browser's own find shortcut must not open on top of the app search.
    expect(fireEvent.keyDown(window, { key: "f", ctrlKey: true })).toBe(false)

    await waitFor(() => expect(document.activeElement).toBe(searchBox()))

    fireEvent.keyDown(window, { key: "Escape" })

    expect(document.activeElement).toBe(document.body)

    // Escape is inert once the search box has given the focus back.
    fireEvent.keyDown(window, { key: "Escape" })

    expect(document.activeElement).toBe(document.body)
})

it("keeps an unsent draft when a query arrives through the address instead", async () => {
    const { user } = renderList()
    await screen.findByText("Nightly container")

    await user.type(searchBox(), "local")
    await waitFor(() => expect(lastPush()).toBe("?query=local"), afterDebounce)

    // Navigation the user did not type, such as a shared link, must not throw
    // away what is already in the box.
    await act(async () => {
        mocks.searchStore.set("query=fromlink")
    })

    await waitFor(() => expect(lastListRequest()).toBe("/workflows?query=fromlink"))
    expect(searchBox()).toHaveProperty("value", "local")
    expect(mocks.push).toHaveBeenCalledTimes(1)
})
