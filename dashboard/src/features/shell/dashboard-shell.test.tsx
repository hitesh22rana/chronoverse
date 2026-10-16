// @vitest-environment jsdom
import { cleanup, render, screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import type { ReactNode } from "react"
import { afterEach, beforeEach, expect, it, vi } from "vitest"
import { Header } from "./header"
import DashboardLayout from "@/app/(dashboard)/layout"

const state = vi.hoisted(() => ({
    notifications: [] as { id: string }[],
    setTheme: vi.fn(),
    useNotifications: vi.fn(),
}))
vi.mock("next-themes", () => ({ useTheme: () => ({ setTheme: state.setTheme }) }))
vi.mock("react", async (importOriginal) => ({
    ...await importOriginal<typeof import("react")>(),
    // Next supplies this experimental boundary; npm React does not.
    ViewTransition: ({ children }: { children: ReactNode }) => children,
}))
vi.mock("@/features/notifications/use-notifications", () => ({
    useNotifications: (options?: unknown) => {
        state.useNotifications(options)
        return {
            notifications: state.notifications, isLoading: false, markAsRead: vi.fn(), refetch: vi.fn(),
            fetchNextPage: vi.fn(), isFetchingNextPage: false, hasNextPage: false,
        }
    },
}))
vi.mock("@/features/users/use-users", () => ({ useUsers: () => ({
    user: { email: "shell@example.com", created_at: "2024-01-01T00:00:00Z", updated_at: "2024-01-01T00:00:00Z", notification_preference: "ALL" },
    updateUser: vi.fn(), isUpdating: false,
}) }))
vi.mock("@/features/auth/use-auth", () => ({ useAuth: () => ({ logout: vi.fn(), isLogoutLoading: false }) }))

beforeEach(() => { vi.clearAllMocks(); state.notifications = [] })
afterEach(cleanup)

it.each([
    [0, "Notifications"],
    [1, "1Notifications"],
    [9, "9Notifications"],
    [10, "9+Notifications"],
] as const)("shows the notification badge text for %i notifications", (count, expected) => {
    state.notifications = Array.from({ length: count }, (_, index) => ({ id: String(index) }))
    render(<Header onNotificationsClick={vi.fn()} onProfileClick={vi.fn()} />)
    const button = screen.getByRole("button", { name: /Notifications/ })
    expect(button.textContent).toBe(expected)
    expect(state.useNotifications).toHaveBeenCalledWith({ poll: true })
    expect(screen.getByRole("link", { name: "Chronoverse" }).getAttribute("href")).toBe("/")
})

it.each(["Light", "Dark", "System"])("selects the %s theme from the real dropdown", async (choice) => {
    const user = userEvent.setup()
    render(<Header onNotificationsClick={vi.fn()} onProfileClick={vi.fn()} />)
    await user.click(screen.getByRole("button", { name: "Toggle theme" }))
    await user.click(await screen.findByRole("menuitem", { name: choice }))
    expect(state.setTheme).toHaveBeenCalledExactlyOnceWith(choice.toLowerCase())
})

it("opens, closes, and reopens the real drawers while preserving page content", async () => {
    const user = userEvent.setup()
    render(<DashboardLayout><h1>Workflow content</h1></DashboardLayout>)
    expect(screen.queryByRole("dialog")).toBeNull()
    await user.click(screen.getByRole("button", { name: "Notifications" }))
    let drawer = await screen.findByRole("dialog", { name: "Notifications" })
    expect(within(drawer).getByRole("heading", { name: /Notifications/ })).toBeTruthy()
    expect(screen.getByRole("heading", { name: "Workflow content", hidden: true })).toBeTruthy()
    await user.click(within(drawer).getByRole("button", { name: "Close" }))
    expect(screen.queryByRole("dialog")).toBeNull()
    await user.click(screen.getByRole("button", { name: "Profile" }))
    drawer = await screen.findByRole("dialog", { name: "Profile" })
    expect(within(drawer).getByText("shell@example.com")).toBeTruthy()
    expect(screen.getByRole("heading", { name: "Workflow content", hidden: true })).toBeTruthy()
    await user.keyboard("{Escape}")
    expect(screen.queryByRole("dialog")).toBeNull()
    await user.click(screen.getByRole("button", { name: "Notifications" }))
    expect(await screen.findByRole("dialog", { name: "Notifications" })).toBeTruthy()
    expect(screen.getByRole("heading", { name: "Workflow content", hidden: true })).toBeTruthy()
})
