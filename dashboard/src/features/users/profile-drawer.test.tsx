// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { act, cleanup, render, screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { afterEach, beforeEach, expect, it, vi } from "vitest"

import { ProfileDrawer } from "./profile-drawer"
import type { User } from "./types"

const mocks = vi.hoisted(() => ({
    fetchApi: vi.fn(),
    fetchApiJson: vi.fn(),
    replace: vi.fn(),
    refresh: vi.fn(),
    toast: { success: vi.fn(), error: vi.fn() },
}))

vi.mock("@/lib/api/client", () => ({ ...mocks, createIdempotencyKey: () => "key" }))
vi.mock("next/navigation", () => ({ useRouter: () => ({ replace: mocks.replace, refresh: mocks.refresh }) }))
vi.mock("sonner", () => ({ toast: mocks.toast }))

const account: User = {
    email: "jane.doe@example.com",
    notification_preference: "ALL",
    created_at: "2024-01-01T00:00:00Z",
    updated_at: "2024-02-01T00:00:00Z",
}

const pointerMembers = ["hasPointerCapture", "setPointerCapture", "releasePointerCapture", "scrollIntoView"] as const
const originals = pointerMembers.map((name) => Object.getOwnPropertyDescriptor(Element.prototype, name))
const clients: QueryClient[] = []

beforeEach(() => {
    vi.clearAllMocks()
    mocks.fetchApi.mockResolvedValue({ ok: true })
    mocks.fetchApiJson.mockImplementation(async (url: string) => url.endsWith("/users") ? { ...account } : { notifications: [], cursor: null })
    Element.prototype.hasPointerCapture = () => false
    Element.prototype.setPointerCapture = () => {}
    Element.prototype.releasePointerCapture = () => {}
    Element.prototype.scrollIntoView = () => {}
})

afterEach(() => {
    cleanup()
    clients.splice(0).forEach((client) => client.clear())
    pointerMembers.forEach((name, index) => {
        const original = originals[index]
        if (original) Object.defineProperty(Element.prototype, name, original)
        else Reflect.deleteProperty(Element.prototype, name)
    })
})

function mount(open = true) {
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 }, mutations: { retry: false } } })
    clients.push(queryClient)
    const onClose = vi.fn()
    const user = userEvent.setup()
    const view = render(<QueryClientProvider client={queryClient}><ProfileDrawer open={open} onClose={onClose} /></QueryClientProvider>)
    return { ...view, user, onClose, queryClient }
}

async function changePreference(user: ReturnType<typeof userEvent.setup>, label = "Important only") {
    await user.click(await screen.findByRole("combobox"))
    await user.click(await screen.findByRole("option", { name: label }))
    return screen.findByRole("dialog", { name: "Confirm Changes" })
}

it("waits for the account and displays its email, initials and dates", async () => {
    let release!: (_user: User) => void
    mocks.fetchApiJson.mockImplementation((url: string) => url.endsWith("/users") ? new Promise<User>((resolve) => { release = resolve }) : Promise.resolve({ notifications: [], cursor: null }))
    mount()
    expect(screen.queryByText("Profile")).toBeNull()

    await act(async () => { release(account) })

    expect(await screen.findByText(account.email)).toBeTruthy()
    expect(screen.getByText("jane.doe")).toBeTruthy()
    expect(screen.getByText("JD")).toBeTruthy()
    expect(screen.getByText(/^Joined /)).toBeTruthy()
    expect(screen.getByText(/Last updated/)).toBeTruthy()
    expect(screen.getByRole("combobox").textContent).toBe("All notifications")
})

it("does not render profile controls while closed", async () => {
    mount(false)
    await waitFor(() => expect(mocks.fetchApiJson).toHaveBeenCalled())
    expect(screen.queryByRole("combobox")).toBeNull()
})

it("shows no profile and reports an account load error", async () => {
    mocks.fetchApiJson.mockRejectedValue(new Error("account unavailable"))
    mount()
    await waitFor(() => expect(mocks.toast.error).toHaveBeenCalledWith("account unavailable"))
    expect(screen.queryByText("Profile")).toBeNull()
})

it("closes the drawer without signing out", async () => {
    const { user, onClose } = mount()
    await screen.findByText(account.email)
    await user.click(screen.getByRole("button", { name: "Close" }))
    expect(onClose).toHaveBeenCalledWith(false)
    expect(mocks.fetchApi).not.toHaveBeenCalled()
})

it("cancels a preference change without writing it", async () => {
    const { user } = mount()
    const dialog = await changePreference(user)
    await user.click(within(dialog).getByRole("button", { name: "Cancel" }))
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "Confirm Changes" })).toBeNull())
    expect(mocks.fetchApi).not.toHaveBeenCalled()
})

it("ignores reselecting the current preference after cancelling", async () => {
    const { user } = mount()
    const dialog = await changePreference(user)
    await user.click(within(dialog).getByRole("button", { name: "Cancel" }))
    await user.click(screen.getByRole("combobox"))
    await user.click(await screen.findByRole("option", { name: "All notifications" }))
    expect(screen.queryByRole("dialog", { name: "Confirm Changes" })).toBeNull()
    expect(mocks.fetchApi).not.toHaveBeenCalled()
})

it.each(["Important only", "No notifications"])("confirms %s and refreshes account and notifications", async (label) => {
    const { user } = mount()
    const dialog = await changePreference(user, label)
    const userReads = mocks.fetchApiJson.mock.calls.filter(([url]) => url === "/users").length
    const notificationReads = mocks.fetchApiJson.mock.calls.filter(([url]) => url === "/notifications").length
    await user.click(within(dialog).getByRole("button", { name: "Confirm" }))
    await waitFor(() => expect(mocks.fetchApi).toHaveBeenCalledWith("/users", "failed to update user", {
        method: "PUT", body: JSON.stringify({ notification_preference: label === "Important only" ? "ALERTS" : "NONE" }),
    }))
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "Confirm Changes" })).toBeNull())
    expect(mocks.toast.success).toHaveBeenCalledWith("user updated successfully")
    await waitFor(() => {
        expect(mocks.fetchApiJson.mock.calls.filter(([url]) => url === "/users").length).toBeGreaterThan(userReads)
        expect(mocks.fetchApiJson.mock.calls.filter(([url]) => url === "/notifications").length).toBeGreaterThan(notificationReads)
    })
})

it("locks confirmation during an update and retries a rejected preference", async () => {
    let reject!: (_error: Error) => void
    mocks.fetchApi.mockImplementationOnce(() => new Promise((_resolve, rejectPromise) => { reject = rejectPromise }))
    const { user } = mount()
    const dialog = await changePreference(user)
    await user.click(within(dialog).getByRole("button", { name: "Confirm" }))
    await waitFor(() => expect(within(dialog).getByRole("button", { name: "Updating..." })).toHaveProperty("disabled", true))
    expect(within(dialog).getByRole("button", { name: "Cancel" })).toHaveProperty("disabled", true)
    await user.click(within(dialog).getByRole("button", { name: "Updating..." }))
    expect(mocks.fetchApi).toHaveBeenCalledTimes(1)
    await act(async () => { reject(new Error("update rejected")) })
    await waitFor(() => expect(mocks.toast.error).toHaveBeenCalledWith("update rejected"))
    expect(screen.getByRole("dialog", { name: "Confirm Changes" })).toBeTruthy()
    await user.click(within(dialog).getByRole("button", { name: "Confirm" }))
    await waitFor(() => expect(mocks.fetchApi).toHaveBeenCalledTimes(2))
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "Confirm Changes" })).toBeNull())
})

it("signs out once, disables the pending button and clears private cached data", async () => {
    let release!: () => void
    mocks.fetchApi.mockImplementationOnce(() => new Promise<void>((resolve) => { release = resolve }))
    const { user, queryClient } = mount()
    queryClient.setQueryData(["private-workflows"], ["private-workflow"])
    await user.click(await screen.findByRole("button", { name: "Sign Out" }))
    await waitFor(() => expect(screen.getByRole("button", { name: "Signing out..." })).toHaveProperty("disabled", true))
    await user.click(screen.getByRole("button", { name: "Signing out..." }))
    expect(mocks.fetchApi).toHaveBeenCalledTimes(1)
    expect(mocks.fetchApi).toHaveBeenCalledWith("/auth/logout", "failed to logout", { method: "POST" })
    await act(async () => { release() })
    await waitFor(() => expect(mocks.replace).toHaveBeenCalledWith("/login"))
    expect(mocks.refresh).toHaveBeenCalledOnce()
    expect(queryClient.getQueryData(["private-workflows"])).toBeUndefined()
})

it("reports sign-out failures and still redirects to login", async () => {
    mocks.fetchApi.mockRejectedValueOnce(new Error("logout rejected"))
    const { user } = mount()
    await user.click(await screen.findByRole("button", { name: "Sign Out" }))
    await waitFor(() => expect(mocks.toast.error).toHaveBeenCalledWith("logout rejected"))
    expect(mocks.replace).toHaveBeenCalledWith("/login")
    expect(mocks.refresh).toHaveBeenCalledOnce()
})
