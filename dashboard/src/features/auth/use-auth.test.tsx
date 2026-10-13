// @vitest-environment jsdom
import { webcrypto } from "node:crypto"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { act, cleanup, renderHook, waitFor } from "@testing-library/react"
import { afterEach, beforeEach, expect, it, vi } from "vitest"
import type { ReactNode } from "react"

import { apiEndpoints } from "@/lib/api/endpoints"
import { queryKeys } from "@/lib/api/query-keys"
import type { User } from "@/features/users/types"
import { useAuth } from "./use-auth"

const mocks = vi.hoisted(() => ({ push: vi.fn(), replace: vi.fn(), refresh: vi.fn(), error: vi.fn() }))
// Preserve other navigation exports.
vi.mock("next/navigation", async (importOriginal) => ({
    ...(await importOriginal<typeof import("next/navigation")>()),
    useRouter: () => mocks,
}))
vi.mock("sonner", () => ({ toast: { error: mocks.error } }))

const credentials = { email: "person@example.com", password: "Orchid!River!Lighthouse!9" }
const user: User = {
    email: "previous@example.com",
    notification_preference: "ALL",
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-02T00:00:00Z",
}
const clients: QueryClient[] = []
const fetchMock = vi.fn<typeof fetch>()

beforeEach(() => {
    vi.clearAllMocks()
    // clearAllMocks leaves queued mockImplementationOnce entries in place.
    fetchMock.mockReset()
    vi.stubGlobal("crypto", webcrypto)
    vi.stubGlobal("fetch", fetchMock)
    document.cookie = "csrf=csrf-token"
    fetchMock.mockResolvedValue(new Response(null, { status: 204 }))
})

afterEach(() => {
    cleanup()
    clients.splice(0).forEach((client) => client.clear())
    document.cookie = "csrf=; max-age=0"
    vi.unstubAllGlobals()
})

function mount() {
    const client = new QueryClient({ defaultOptions: { mutations: { retry: false }, queries: { retry: false, gcTime: Infinity } } })
    clients.push(client)
    client.setQueryData(queryKeys.user, user)
    const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>
    return { ...renderHook(() => useAuth(), { wrapper }), client }
}

function request(index = 0) {
    const [url, options] = fetchMock.mock.calls[index]
    return { url, ...options, headers: new Headers(options?.headers) }
}

it("posts login credentials with CSRF and cookies, then clears account data and navigates", async () => {
    const { result, client } = mount()
    act(() => result.current.login(credentials))
    await waitFor(() => expect(mocks.push).toHaveBeenCalledWith("/"))
    expect(fetchMock).toHaveBeenCalledTimes(1)
    expect(request()).toMatchObject({ url: apiEndpoints.auth.login, method: "POST", credentials: "include", body: JSON.stringify(credentials) })
    expect(request().headers.get("X-CSRF-Token")).toBe("csrf-token")
    expect(client.getQueryCache().getAll()).toEqual([])
    expect(mocks.replace).not.toHaveBeenCalled()
    expect(mocks.error).not.toHaveBeenCalled()
})

it("keeps the account and route on login rejection without exposing credentials", async () => {
    fetchMock.mockResolvedValue(new Response("credentials rejected", { status: 401 }))
    const { result, client } = mount()
    act(() => result.current.login(credentials))
    await waitFor(() => expect(mocks.error).toHaveBeenCalledWith("failed to login: credentials rejected"))
    expect(client.getQueryData(queryKeys.user)).toEqual(user)
    expect(mocks.push).not.toHaveBeenCalled()
    expect(JSON.stringify(mocks.error.mock.calls)).not.toContain(credentials.password)
})

it.each([true, false])("logout redirects and refreshes even when the request succeeds=%s", async (success) => {
    fetchMock.mockResolvedValue(new Response(success ? null : "session expired", { status: success ? 204 : 401 }))
    const { result, client } = mount()
    act(() => result.current.logout())
    await waitFor(() => expect(mocks.refresh).toHaveBeenCalledTimes(1))
    expect(mocks.replace).toHaveBeenCalledExactlyOnceWith("/login")
    expect(mocks.push).not.toHaveBeenCalled()
    expect(request()).toMatchObject({ url: apiEndpoints.auth.logout, method: "POST", credentials: "include" })
    expect(request().body).toBeUndefined()
    if (success) expect(client.getQueryCache().getAll()).toEqual([])
    else expect(client.getQueryData(queryKeys.user)).toEqual(user)
    if (success) expect(mocks.error).not.toHaveBeenCalled()
    else expect(mocks.error).toHaveBeenCalledExactlyOnceWith("failed to logout: session expired")
})

it("reuses failed signup identity, rotates changed credentials, and resets after success", async () => {
    fetchMock.mockResolvedValueOnce(new Response("retry later", { status: 503 }))
    const { result, client } = mount()
    act(() => result.current.signup(credentials))
    await waitFor(() => expect(mocks.error).toHaveBeenCalledTimes(1))
    const firstKey = request().headers.get("Idempotency-Key")
    expect(firstKey).toBeTruthy()
    expect(request()).toMatchObject({ url: apiEndpoints.auth.signup, method: "POST", credentials: "include", body: JSON.stringify(credentials) })
    expect(client.getQueryData(queryKeys.user)).toEqual(user)
    act(() => result.current.signup(credentials))
    await waitFor(() => expect(mocks.push).toHaveBeenCalledTimes(1))
    expect(request(1).headers.get("Idempotency-Key")).toBe(firstKey)
    expect(client.getQueryCache().getAll()).toEqual([])
    act(() => result.current.signup(credentials))
    await waitFor(() => expect(mocks.push).toHaveBeenCalledTimes(2))
    expect(request(2).headers.get("Idempotency-Key")).not.toBe(firstKey)
    fetchMock.mockResolvedValueOnce(new Response("retry later", { status: 503 }))
    act(() => result.current.signup(credentials))
    await waitFor(() => expect(mocks.error).toHaveBeenCalledTimes(2))
    const failedKey = request(3).headers.get("Idempotency-Key")
    act(() => result.current.signup({ ...credentials, password: "Different!Secret!9" }))
    await waitFor(() => expect(mocks.push).toHaveBeenCalledTimes(3))
    expect(request(4).headers.get("Idempotency-Key")).not.toBe(failedKey)
    expect(JSON.stringify(mocks.error.mock.calls)).not.toContain(credentials.password)
})

it.each([new Error("digest unavailable"), "digest unavailable"])("does not submit credentials if fingerprinting rejects: %s", async (failure) => {
    vi.stubGlobal("crypto", { subtle: { digest: vi.fn().mockRejectedValue(failure) } })
    const { result, client } = mount()
    act(() => result.current.signup(credentials))
    await waitFor(() => expect(mocks.error).toHaveBeenCalledExactlyOnceWith(failure instanceof Error ? failure.message : "failed to prepare signup"))
    expect(fetchMock).not.toHaveBeenCalled()
    expect(client.getQueryData(queryKeys.user)).toEqual(user)
    expect(mocks.push).not.toHaveBeenCalled()
    expect(JSON.stringify(mocks.error.mock.calls)).not.toContain(credentials.password)
})

it("an older signup success preserves a newer failed attempt for retry", async () => {
    let finishFirst!: (_response: Response) => void
    fetchMock.mockImplementationOnce(() => new Promise<Response>((resolve) => { finishFirst = resolve }))
    fetchMock.mockResolvedValueOnce(new Response("retry later", { status: 503 }))
    const { result } = mount()
    act(() => result.current.signup(credentials))
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1))
    const newerCredentials = { ...credentials, email: "new@example.com" }
    act(() => result.current.signup(newerCredentials))
    await waitFor(() => expect(mocks.error).toHaveBeenCalledTimes(1))
    const newerKey = request(1).headers.get("Idempotency-Key")
    expect(newerKey).not.toBe(request().headers.get("Idempotency-Key"))
    await act(async () => finishFirst(new Response(null, { status: 204 })))
    await waitFor(() => expect(mocks.push).toHaveBeenCalledTimes(1))
    act(() => result.current.signup(newerCredentials))
    await waitFor(() => expect(mocks.push).toHaveBeenCalledTimes(2))
    expect(request(2).headers.get("Idempotency-Key")).toBe(newerKey)
})

it("keeps login pending and account data intact until the server responds", async () => {
    let respond!: (_response: Response) => void
    fetchMock.mockImplementationOnce(() => new Promise<Response>((resolve) => { respond = resolve }))
    const { result, client } = mount()
    act(() => result.current.login(credentials))
    await waitFor(() => expect(result.current.isLoginLoading).toBe(true))
    expect(client.getQueryData(queryKeys.user)).toEqual(user)
    expect(mocks.push).not.toHaveBeenCalled()
    await act(async () => respond(new Response(null, { status: 204 })))
    await waitFor(() => expect(result.current.isLoginLoading).toBe(false))
    expect(mocks.push).toHaveBeenCalledExactlyOnceWith("/")
})

it("keeps logout pending and the cached user until the server responds", async () => {
    let respond!: (_response: Response) => void
    fetchMock.mockImplementationOnce(() => new Promise<Response>((resolve) => { respond = resolve }))
    const { result, client } = mount()
    act(() => result.current.logout())
    await waitFor(() => expect(result.current.isLogoutLoading).toBe(true))
    expect(client.getQueryData(queryKeys.user)).toEqual(user)
    expect(mocks.replace).not.toHaveBeenCalled()
    expect(mocks.refresh).not.toHaveBeenCalled()
    await act(async () => respond(new Response(null, { status: 204 })))
    await waitFor(() => expect(result.current.isLogoutLoading).toBe(false))
    expect(mocks.replace).toHaveBeenCalledExactlyOnceWith("/login")
    expect(mocks.refresh).toHaveBeenCalledTimes(1)
    expect(client.getQueryCache().getAll()).toEqual([])
})

it("keeps signup pending and the cached user until the server responds", async () => {
    let respond!: (_response: Response) => void
    fetchMock.mockImplementationOnce(() => new Promise<Response>((resolve) => { respond = resolve }))
    const { result, client } = mount()
    // Fingerprinting resolves before mutate, so pending starts once the request is in flight.
    act(() => result.current.signup(credentials))
    expect(result.current.isSignupLoading).toBe(false)
    await waitFor(() => expect(result.current.isSignupLoading).toBe(true))
    expect(client.getQueryData(queryKeys.user)).toEqual(user)
    expect(mocks.push).not.toHaveBeenCalled()
    await act(async () => respond(new Response(null, { status: 204 })))
    await waitFor(() => expect(result.current.isSignupLoading).toBe(false))
    expect(mocks.push).toHaveBeenCalledExactlyOnceWith("/")
    expect(client.getQueryCache().getAll()).toEqual([])
})

it.each([
    { crypto: {}, message: "secure signup retry identification is unavailable" },
    { crypto: { subtle: webcrypto.subtle }, message: "secure idempotency key generation is unavailable" },
])("fails safely when signup cryptography is unavailable: $message", async ({ crypto, message }) => {
    vi.stubGlobal("crypto", crypto)
    const { result } = mount()
    act(() => result.current.signup(credentials))
    await waitFor(() => expect(mocks.error).toHaveBeenCalledExactlyOnceWith(message))
    expect(fetchMock).not.toHaveBeenCalled()
    expect(mocks.push).not.toHaveBeenCalled()
})
