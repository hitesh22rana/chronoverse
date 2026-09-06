import { afterEach, beforeEach, expect, it, vi } from "vitest"

const mocks = vi.hoisted(() => ({
    effects: [] as (() => void | (() => void))[],
    query: vi.fn(),
    params: new URLSearchParams(),
    setLogs: vi.fn(),
    push: vi.fn(),
    workflow: undefined as { kind: string, log_retention: boolean } | undefined,
}))

vi.mock("react", () => ({
    useEffect: (effect: () => void | (() => void)) => mocks.effects.push(effect),
    useState: (initial: unknown) => [initial, mocks.setLogs],
}))
vi.mock("next/navigation", () => ({
    usePathname: () => "/workflows/w/jobs/j",
    useRouter: () => ({ push: mocks.push }),
    useSearchParams: () => mocks.params,
}))
vi.mock("@tanstack/react-query", () => ({
    useInfiniteQuery: mocks.query,
    useMutation: () => ({ isPending: false }),
}))
vi.mock("@/features/workflows/use-workflow-details", () => ({
    useWorkflowDetails: () => ({ workflow: mocks.workflow }),
}))
vi.mock("@/lib/api/client", () => ({ fetchApi: vi.fn(), fetchApiJson: vi.fn() }))

import { useJobLogs } from "./use-job-logs"

const log = { event_id: "e:1", sequence_num: 1, message: "saved", timestamp: "", stream: "stdout" }
const retained = { data: { pages: [{ logs: [log] }] }, fetchNextPage: vi.fn(), isLoading: true, hasNextPage: true }
const searched = { ...retained, data: { pages: [{ logs: [{ ...log, message: "found" }] }] } }

beforeEach(() => {
    mocks.workflow = { kind: "CONTAINER", log_retention: true }
    mocks.push.mockClear()
    mocks.effects.length = 0
    mocks.params = new URLSearchParams()
    mocks.setLogs.mockClear()
    mocks.query.mockReset().mockReturnValueOnce(retained).mockReturnValueOnce(searched)
})

afterEach(() => vi.unstubAllGlobals())

it.each(["RUNNING", "COMPLETED", "FAILED", "CANCELED"])("returns retained logs for %s", (status) => {
    const result = useJobLogs("w", "j", status)
    expect(result.logs).toEqual([log])
    expect(result.fetchNextPage).toBe(retained.fetchNextPage)
    expect(result.isLoading).toBe(true)
})

it.each(["PENDING", "QUEUED"])("returns an empty state for %s", (status) => {
    const result = useJobLogs("w", "j", status)
    expect(result.logs).toEqual([])
    expect(result.isLoading).toBe(false)
    expect(result.hasNextPage).toBe(false)
})

it("uses search results when a filter is present", () => {
    mocks.params.set("stream", "stderr")
    expect(useJobLogs("w", "j", "RUNNING").logs[0].message).toBe("found")
})

it("opens a credentialed native stream, normalizes logs, and cleans up", () => {
    const source = Object.assign(new EventTarget(), { close: vi.fn() })
    const constructor = vi.fn(function () { return source })
    vi.stubGlobal("EventSource", constructor)
    useJobLogs("w", "j", "RUNNING")
    const cleanup = mocks.effects[0]()
    expect(constructor).toHaveBeenCalledWith(expect.stringContaining("/events"), { withCredentials: true })

    source.dispatchEvent(new MessageEvent("log", { data: JSON.stringify(log) }))
    expect(mocks.setLogs.mock.calls[0][0]([])).toEqual([log])
    source.dispatchEvent(new MessageEvent("log", { data: "invalid json" }))
    expect(mocks.setLogs).toHaveBeenCalledTimes(1)

    if (typeof cleanup !== "function") throw new Error("missing stream cleanup")
    cleanup()
    expect(source.close).toHaveBeenCalledOnce()
    source.dispatchEvent(new MessageEvent("log", { data: JSON.stringify(log) }))
    expect(mocks.setLogs).toHaveBeenCalledTimes(1)
})

it.each(["COMPLETED", "QUEUED", "filtered"])("does not open a stream for %s", (state) => {
    const constructor = vi.fn()
    vi.stubGlobal("EventSource", constructor)
    if (state === "filtered") mocks.params.set("q", "error")
    useJobLogs("w", "j", state === "filtered" ? "RUNNING" : state)
    expect(mocks.effects[0]()).toBeUndefined()
    expect(constructor).not.toHaveBeenCalled()
})

it.each([
    [undefined, false, false],
    [{ kind: "HEARTBEAT", log_retention: false }, true, false],
    [{ kind: "CONTAINER", log_retention: false }, false, true],
] as const)("does not fetch unavailable logs for %j", (workflow, unsupported, disabled) => {
    mocks.workflow = workflow
    const result = useJobLogs("w", "j", "RUNNING")
    expect(result.isLogsUnsupportedForKind).toBe(unsupported)
    expect(result.isRetentionDisabled).toBe(disabled)
    expect(mocks.query.mock.calls.map(([options]) => options.enabled)).toEqual([false, false])
    expect(mocks.effects[0]()).toBeUndefined()
})

it("updates and clears filters while preserving unrelated URL parameters", () => {
    mocks.params = new URLSearchParams("json=true&q=old&stream=stderr")
    const result = useJobLogs("w", "j", "RUNNING")
    result.updateSearchQuery("new value")
    result.updateSearchQuery("")
    result.applyStreamFilter("stdout")
    result.applyStreamFilter("")
    expect(mocks.push.mock.calls.flat()).toEqual([
        "/workflows/w/jobs/j?json=true&q=new+value&stream=stderr",
        "/workflows/w/jobs/j?json=true&stream=stderr",
        "/workflows/w/jobs/j?json=true&q=old&stream=stdout",
        "/workflows/w/jobs/j?json=true&q=old",
    ])
})
