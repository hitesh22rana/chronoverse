// @vitest-environment jsdom
/**
 * Mounted tests for the workflow list page: the shell it renders around the
 * list, how the create button opens the real three-step dialog, and how the
 * lazily loaded analytics drawer replaces its placeholder.
 *
 * Boundaries only: the transport, the Next.js navigation hooks and `sonner`.
 * React Query, `next/dynamic`, Radix and the create dialog all run for real, so
 * these tests cover the page-level wiring the feature's own tests cannot see.
 */
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { cleanup, render, screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { useSyncExternalStore } from "react"
import { afterEach, beforeEach, expect, it, vi } from "vitest"

import WorkflowsPage from "./workflows-page"

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
        push: vi.fn(),
        back: vi.fn(),
        replace: vi.fn(),
        fetchApi: vi.fn(),
        fetchApiJson: vi.fn(),
        toast: { error: vi.fn(), success: vi.fn(), warning: vi.fn() },
        list: null as unknown,
    }
})

vi.mock("next/navigation", () => ({
    usePathname: () => "/",
    useRouter: () => ({ push: mocks.push, back: mocks.back, replace: mocks.replace }),
    useSearchParams: () => {
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

function renderPage() {
    mocks.searchStore.set("")
    const queryClient = new QueryClient({
        defaultOptions: { queries: { retry: false, gcTime: 0 }, mutations: { retry: false } },
    })
    const user = userEvent.setup()
    const view = render(
        <QueryClientProvider client={queryClient}>
            <WorkflowsPage />
        </QueryClientProvider>,
    )
    return { ...view, user, queryClient }
}

const requestedUrls = () => mocks.fetchApiJson.mock.calls.map((call) => String(call[0]))

/** The lazily imported drawer's trigger only exists once the import resolves. */
const analyticsTrigger = async () =>
    screen.findByRole("button", { name: /Analytics/ }, { timeout: 5000 })

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
    if (!Element.prototype.scrollTo) {
        Element.prototype.scrollTo = () => {}
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
    mocks.list = {
        workflows: [
            {
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
            },
        ],
    }
    mocks.searchStore.set("")
    mocks.push.mockClear()
    mocks.back.mockClear()
    mocks.replace.mockClear()
    mocks.toast.error.mockClear()
    mocks.push.mockImplementation((url: string) => {
        mocks.searchStore.set(url.replace(/^[?]/, ""))
    })
    mocks.fetchApi.mockClear().mockResolvedValue({ ok: true, status: 201 })
    mocks.fetchApiJson.mockReset().mockImplementation(async (url: string) => {
        // The drawer's own query stays out of the way of the list assertions.
        if (url.startsWith("/analytics")) return { total_workflows: 0, total_jobs: 0, total_joblogs: 0, total_job_execution_duration: 0, workflow_kinds: [], top_workflows: [] }
        return mocks.list
    })
    installDomStubs()
})

afterEach(() => {
    cleanup()
    vi.unstubAllGlobals()
    restoreDomStubs()
})

it("frames the workflow list with its heading and the two page actions", async () => {
    renderPage()

    expect(screen.getByRole("heading", { name: "Dashboard" })).toBeTruthy()
    expect(screen.getByText("Monitor and manage your automated workflows")).toBeTruthy()
    expect(screen.getByRole("button", { name: /Create workflow/ })).toBeTruthy()
    // The list the feature owns is rendered inside this shell.
    expect(await screen.findByText("Nightly container")).toBeTruthy()
    expect(screen.getByPlaceholderText("Search workflows...(Ctrl+F)")).toBeTruthy()
    // The drawer arrives through `next/dynamic`, so it only exists after the
    // import resolves; before that the placeholder is hidden from assistive tech.
    expect(await analyticsTrigger()).toBeTruthy()
})

it("opens the real create dialog from the page button and closes it again", async () => {
    const { user } = renderPage()
    await screen.findByText("Nightly container")

    expect(screen.queryByText("Create new workflow")).toBeNull()

    await user.click(screen.getByRole("button", { name: /Create workflow/ }))

    // The page mounts the actual dialog, not a stub: step one is on screen.
    const dialog = await screen.findByRole("dialog")
    expect(within(dialog).getByText("Create new workflow")).toBeTruthy()
    expect(within(dialog).getByText("Step 1 of 3")).toBeTruthy()
    expect(within(dialog).getByRole("list", { name: "Workflow setup progress" })).toBeTruthy()
    // Opening the dialog creates nothing on its own.
    expect(mocks.fetchApi).not.toHaveBeenCalled()

    await user.click(within(dialog).getByRole("button", { name: "Cancel" }))

    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
    expect(mocks.fetchApi).not.toHaveBeenCalled()
    // The list underneath is untouched by opening and closing the dialog.
    expect(screen.getByText("Nightly container")).toBeTruthy()
})

it("keeps the create dialog's first step validation on the page path", async () => {
    const { user } = renderPage()
    await screen.findByText("Nightly container")

    await user.click(screen.getByRole("button", { name: /Create workflow/ }))
    const dialog = await screen.findByRole("dialog")
    await user.click(within(dialog).getByRole("button", { name: /Next/ }))

    expect(await within(dialog).findByText("Name must be at least 3 characters")).toBeTruthy()
    expect(within(dialog).getByText("Step 1 of 3")).toBeTruthy()
    expect(mocks.fetchApi).not.toHaveBeenCalled()
})

it("does not ask for analytics until the drawer is opened", async () => {
    const { user } = renderPage()
    await screen.findByText("Nightly container")

    // The page itself only needs the workflow list.
    expect(requestedUrls()).toEqual(["/workflows"])

    await user.click(await analyticsTrigger())

    expect(await screen.findByText("Analytics overview")).toBeTruthy()
    await waitFor(() => expect(requestedUrls()).toContain("/analytics"))
    // Opening the drawer does not refetch the workflow list behind it.
    expect(requestedUrls().filter((url: string) => url === "/workflows")).toHaveLength(1)
})
