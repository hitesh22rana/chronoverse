// @vitest-environment jsdom
/**
 * Mounted lifecycle tests for the live log stream and the raw download owned by
 * `useJobLogs`.
 *
 * Only boundaries are faked: the transport (`@/lib/api/client`), the Next.js
 * navigation hooks, `sonner`, the browser `EventSource` constructor that jsdom
 * does not implement, and the anchor download/`URL.createObjectURL` pair that
 * jsdom does not implement either. The hook, react-query and the log merge
 * helpers run for real, so every assertion is an observable rendering or
 * callback outcome instead of an implementation call-order mock.
 */
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { act, cleanup, render, screen, waitFor } from "@testing-library/react"
import { useEffect, useState } from "react"
import { afterEach, beforeEach, expect, it, vi } from "vitest"
import type { MockInstance } from "vitest"

import type { JobLog } from "./types"
import { useJobLogs } from "./use-job-logs"

const mocks = vi.hoisted(() => ({
    pathname: "/workflows/w1/jobs/j1",
    search: "",
    push: vi.fn(),
    replace: vi.fn(),
    fetchApi: vi.fn(),
    fetchApiJson: vi.fn(),
    toast: { error: vi.fn(), success: vi.fn(), warning: vi.fn() },
    sources: [] as unknown[],
    notify: null as null | (() => void),
    downloads: [] as HTMLAnchorElement[],
    anchorClick: null as unknown as MockInstance<() => void>,
    objectUrls: [] as string[],
    revoked: [] as string[],
}))

class FakeEventSource {
    static CONNECTING = 0
    static OPEN = 1
    static CLOSED = 2

    readonly url: string
    readonly init: unknown
    readyState: number = FakeEventSource.OPEN
    closed = false
    onerror: (() => void) | null = null
    readonly registered = new Map<string, Set<(_event: unknown) => void>>()
    readonly removed: string[] = []

    constructor(url: string, init?: unknown) {
        this.url = url
        this.init = init
        mocks.sources.push(this)
    }

    addEventListener(type: string, listener: (_event: unknown) => void) {
        const listeners = this.registered.get(type) ?? new Set()
        listeners.add(listener)
        this.registered.set(type, listeners)
    }

    removeEventListener(type: string, listener: (_event: unknown) => void) {
        this.removed.push(type)
        this.registered.get(type)?.delete(listener)
    }

    close() {
        this.closed = true
        this.readyState = FakeEventSource.CLOSED
        this.registered.clear()
    }

    emitLog(payload: unknown) {
        const data = typeof payload === "string" ? payload : JSON.stringify(payload)
        this.dispatch("log", new MessageEvent("log", { data }))
    }

    emitStreamError() {
        this.dispatch("error", new Event("error"))
    }

    private dispatch(type: string, event: unknown) {
        for (const listener of [...(this.registered.get(type) ?? [])]) {
            listener(event)
        }
    }
}

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

const retainedLog = (sequenceNum: number) => ({
    event_id: `e:${sequenceNum}`,
    sequence_num: sequenceNum,
    message: `retained line ${sequenceNum}`,
    timestamp: new Date(Date.UTC(2026, 0, 1, 0, 0, sequenceNum)).toISOString(),
    stream: "stdout",
})

const liveWire = (sequenceNum: number, overrides: Record<string, unknown> = {}) => ({
    event_id: `e:${sequenceNum}`,
    sequence_num: sequenceNum,
    message: `live line ${sequenceNum}`,
    timestamp: new Date(Date.UTC(2026, 0, 1, 0, 1, sequenceNum)).toISOString(),
    stream: "stdout",
    ...overrides,
})

const sequences = () =>
    [...screen.getAllByRole("listitem")].map((row) => row.getAttribute("data-sequence"))

function JobLogsHarness({ jobStatus }: { jobStatus: string }) {
    const { logs, isLoading, error, updateSearchQuery, downloadLogsMutation, isDownloadLogsMutationLoading } =
        useJobLogs("w1", "j1", jobStatus)
    return (
        <div>
            <p data-testid="loading">{String(isLoading)}</p>
            <p data-testid="error">{error ? error.message : "none"}</p>
            <p data-testid="downloading">{String(isDownloadLogsMutationLoading)}</p>
            <button type="button" onClick={() => updateSearchQuery("timeout")}>search timeout</button>
            <button type="button" onClick={() => updateSearchQuery("")}>clear search</button>
            <button type="button" onClick={() => downloadLogsMutation.mutate({ filename: "job-logs.txt", format: "json" })}>
                download json
            </button>
            <ul>
                {logs.map((log: JobLog) => (
                    <li key={`${log.event_id}-${log.sequence_num}`} data-sequence={log.sequence_num}>
                        {log.message}
                    </li>
                ))}
            </ul>
        </div>
    )
}

function renderJobLogs(jobStatus: string) {
    const queryClient = new QueryClient({
        defaultOptions: { queries: { retry: false, gcTime: 0 }, mutations: { retry: false } },
    })

    // The harness subscribes so the fake router can re-render the view after a
    // push, the way the Next.js router would after a real navigation.
    function Harness({ status }: { status: string }) {
        const [, setTick] = useState(0)
        useEffect(() => {
            mocks.notify = () => setTick((tick) => tick + 1)
            return () => {
                mocks.notify = null
            }
        }, [])
        return <JobLogsHarness jobStatus={status} />
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

beforeEach(() => {
    mocks.pathname = "/workflows/w1/jobs/j1"
    mocks.search = ""
    mocks.sources.length = 0
    mocks.notify = null
    mocks.push.mockClear().mockImplementation((url: string) => {
        const parsed = new URL(url, "http://localhost")
        mocks.pathname = parsed.pathname
        mocks.search = parsed.searchParams.toString()
        mocks.notify?.()
    })
    mocks.replace.mockClear()
    mocks.fetchApi.mockReset()
    mocks.fetchApiJson.mockReset().mockImplementation(async (url: string) => {
        if (url.includes("/logs/search")) {
            return { id: "j1", workflow_id: "w1", logs: [retainedLog(2), retainedLog(1)], highlight_token: "timeout" }
        }
        if (url.includes("/logs")) {
            return { id: "j1", workflow_id: "w1", logs: [retainedLog(3), retainedLog(2), retainedLog(1)] }
        }
        if (url.includes("/workflows/w1")) {
            return workflow
        }
        throw new Error(`unexpected transport call: ${url}`)
    })
    mocks.toast.error.mockClear()
    mocks.toast.success.mockClear()
    mocks.toast.warning.mockClear()
    mocks.downloads.length = 0
    mocks.objectUrls.length = 0
    mocks.revoked.length = 0
    // jsdom implements neither object URLs nor anchor downloads; stand in for
    // the browser download surface only.
    const UrlWithObjectUrls = class extends URL {}
    const urlStub = UrlWithObjectUrls as unknown as { createObjectURL: () => string, revokeObjectURL: (_url: string) => void }
    urlStub.createObjectURL = () => {
        const url = `blob:http://localhost/${mocks.objectUrls.length + 1}`
        mocks.objectUrls.push(url)
        return url
    }
    urlStub.revokeObjectURL = (url: string) => {
        mocks.revoked.push(url)
    }
    vi.stubGlobal("URL", UrlWithObjectUrls)
    // jsdom cannot perform a download; the anchor the hook builds is still
    // observable through the click it triggers.
    mocks.anchorClick = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => {
        mocks.downloads.push(vi.mocked(HTMLAnchorElement.prototype.click).mock.instances.at(-1) as HTMLAnchorElement)
    })
    vi.stubGlobal("EventSource", FakeEventSource)
})

afterEach(() => {
    cleanup()
    vi.restoreAllMocks()
    vi.unstubAllGlobals()
})

it("renders live stream messages merged with retained logs", async () => {
    renderJobLogs("RUNNING")
    await waitFor(() => expect(screen.getAllByRole("listitem")).toHaveLength(3))

    const source = mocks.sources[0] as FakeEventSource
    expect(source.url).toBe("/workflows/w1/jobs/j1/events")
    expect(source.init).toEqual({ withCredentials: true })

    await act(async () => {
        source.emitLog(liveWire(4))
    })
    await waitFor(() => expect(sequences()).toEqual(["4", "3", "2", "1"]))
    expect(screen.getByText("live line 4")).toBeTruthy()

    // A replayed stream event, and a stream event that repeats an already
    // retained event id, both resolve to a single row.
    await act(async () => {
        source.emitLog(liveWire(4, { message: "live line 4 replayed" }))
        source.emitLog(liveWire(3, { message: "live line 3 replayed" }))
    })
    expect(sequences()).toEqual(["4", "3", "2", "1"])

    // Undecodable payloads are dropped rather than breaking the stream.
    await act(async () => {
        source.emitLog("not json")
    })
    expect(sequences()).toEqual(["4", "3", "2", "1"])
})

it("closes the stream and stops applying logs when the job leaves the running state", async () => {
    const view = renderJobLogs("RUNNING")
    await waitFor(() => expect(mocks.sources).toHaveLength(1))
    const source = mocks.sources[0] as FakeEventSource

    await act(async () => {
        view.setJobStatus("COMPLETED")
    })

    await waitFor(() => expect(source.closed).toBe(true))
    expect(source.removed).toEqual(expect.arrayContaining(["log", "error"]))
    expect(mocks.sources).toHaveLength(1)

    await act(async () => {
        source.emitLog(liveWire(9))
    })
    expect(screen.queryByText("live line 9")).toBeNull()
})

it("closes the stream when the view unmounts", async () => {
    const view = renderJobLogs("RUNNING")
    await waitFor(() => expect(mocks.sources).toHaveLength(1))
    const source = mocks.sources[0] as FakeEventSource

    view.unmount()

    expect(source.closed).toBe(true)
    expect(source.removed).toEqual(expect.arrayContaining(["log", "error"]))
})

it("pauses streaming while a search filter is active and resumes with a fresh stream", async () => {
    renderJobLogs("RUNNING")
    await waitFor(() => expect(mocks.sources).toHaveLength(1))
    const first = mocks.sources[0] as FakeEventSource

    await act(async () => {
        screen.getByText("search timeout").click()
    })

    await waitFor(() => expect(first.closed).toBe(true))
    expect(mocks.sources).toHaveLength(1)
    await waitFor(() => expect(sequences()).toEqual(["2", "1"]))

    await act(async () => {
        screen.getByText("clear search").click()
    })

    await waitFor(() => expect(mocks.sources).toHaveLength(2))
    const second = mocks.sources[1] as FakeEventSource
    expect(second.url).toBe("/workflows/w1/jobs/j1/events")
    await waitFor(() => expect(sequences()).toEqual(["3", "2", "1"]))
})

it("reports stream failures and stays silent for a normal close", async () => {
    renderJobLogs("RUNNING")
    await waitFor(() => expect(mocks.sources).toHaveLength(1))
    const source = mocks.sources[0] as FakeEventSource

    await act(async () => {
        source.emitStreamError()
    })
    expect(mocks.toast.error).toHaveBeenCalledWith("Log streaming error occurred")

    mocks.toast.error.mockClear()
    source.readyState = FakeEventSource.CONNECTING
    await act(async () => {
        source.onerror?.()
    })
    expect(mocks.toast.error).toHaveBeenCalledWith("Lost connection to log stream")

    mocks.toast.error.mockClear()
    source.readyState = FakeEventSource.CLOSED
    await act(async () => {
        source.onerror?.()
    })
    expect(mocks.toast.error).not.toHaveBeenCalled()
})

it("reports a retained log read failure and keeps the error visible", async () => {
    mocks.fetchApiJson.mockImplementation(async (url: string) => {
        if (url.includes("/logs")) {
            throw new Error("failed to fetch job logs: boom")
        }
        return workflow
    })

    renderJobLogs("COMPLETED")

    await waitFor(() => expect(screen.getByTestId("error").textContent).toBe("failed to fetch job logs: boom"))
    expect(mocks.toast.error).toHaveBeenCalledWith("failed to fetch job logs: boom")
    expect(mocks.sources).toHaveLength(0)
})

it("reports a failed search read instead of the retained read", async () => {
    renderJobLogs("COMPLETED")
    await waitFor(() => expect(screen.getAllByRole("listitem")).toHaveLength(3))

    mocks.fetchApiJson.mockImplementation(async (url: string) => {
        if (url.includes("/logs/search")) {
            throw new Error("failed to fetch job logs: search backend down")
        }
        if (url.includes("/logs")) {
            return { id: "j1", workflow_id: "w1", logs: [retainedLog(3), retainedLog(2), retainedLog(1)] }
        }
        return workflow
    })

    await act(async () => {
        screen.getByText("search timeout").click()
    })

    await waitFor(() => expect(mocks.toast.error).toHaveBeenCalledWith("failed to fetch job logs: search backend down"))
    expect(screen.getByTestId("error").textContent).toBe("failed to fetch job logs: search backend down")
    // A failed search must not resurrect retained rows as if they matched.
    expect(screen.queryByText("retained line 1")).toBeNull()
})

it("downloads the retained logs under a normalized file name and releases the object URL", async () => {
    mocks.fetchApi.mockImplementation(async () => ({ blob: async () => new Blob(["logs"]) }))
    renderJobLogs("COMPLETED")
    await waitFor(() => expect(screen.getAllByRole("listitem")).toHaveLength(3))

    await act(async () => {
        screen.getByText("download json").click()
    })

    await waitFor(() => expect(mocks.downloads).toHaveLength(1))
    expect(mocks.fetchApi).toHaveBeenCalledWith("/workflows/w1/jobs/j1/logs/raw?format=json", "failed to download logs")
    // The requested .txt suffix is replaced by the requested format.
    const anchor = mocks.downloads[0]
    expect(anchor.download).toBe("job-logs.json")
    expect(anchor.href).toBe("blob:http://localhost/1")
    expect(mocks.revoked).toEqual(["blob:http://localhost/1"])
    expect(document.querySelector("a[download]")).toBeNull()
    expect(mocks.toast.success).toHaveBeenCalledWith("Logs downloaded successfully")
    await waitFor(() => expect(screen.getByTestId("downloading").textContent).toBe("false"))
})

it("scopes the log download to the active search filter", async () => {
    mocks.fetchApi.mockImplementation(async () => ({ blob: async () => new Blob(["logs"]) }))
    renderJobLogs("COMPLETED")
    await waitFor(() => expect(screen.getAllByRole("listitem")).toHaveLength(3))

    await act(async () => {
        screen.getByText("search timeout").click()
    })
    await waitFor(() => expect(sequences()).toEqual(["2", "1"]))
    await act(async () => {
        screen.getByText("download json").click()
    })

    await waitFor(() => expect(mocks.fetchApi).toHaveBeenCalled())
    expect(mocks.fetchApi).toHaveBeenCalledWith(
        "/workflows/w1/jobs/j1/logs/raw?q=timeout&format=json",
        "failed to download logs",
    )
})

it("reports a failed download and clears the pending state", async () => {
    mocks.fetchApi.mockImplementation(async () => {
        throw new Error("failed to download logs: 500")
    })
    renderJobLogs("COMPLETED")
    await waitFor(() => expect(screen.getAllByRole("listitem")).toHaveLength(3))

    await act(async () => {
        screen.getByText("download json").click()
    })

    await waitFor(() => expect(mocks.toast.error).toHaveBeenCalledWith("failed to download logs: 500"))
    expect(mocks.downloads).toHaveLength(0)
    await waitFor(() => expect(screen.getByTestId("downloading").textContent).toBe("false"))
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
