// @vitest-environment jsdom
import { cleanup, render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { afterEach, beforeEach, expect, it, vi } from "vitest"

import LoginPage from "./login-page"
import SignupPage from "./signup-page"

const mocks = vi.hoisted(() => ({ login: vi.fn(), signup: vi.fn(), isLoginLoading: false, isSignupLoading: false }))
vi.mock("./use-auth", () => ({ useAuth: () => mocks }))

beforeEach(() => {
    vi.clearAllMocks()
    mocks.isLoginLoading = false
    mocks.isSignupLoading = false
})
afterEach(cleanup)

it("validates required login fields and submits only email and password", async () => {
    const user = userEvent.setup()
    render(<LoginPage />)
    await user.click(screen.getByRole("button", { name: "Sign in" }))
    expect(await screen.findByText("Password is required")).toBeTruthy()
    expect(screen.getByText("Please enter a valid email")).toBeTruthy()
    expect(mocks.login).not.toHaveBeenCalled()
    await user.type(screen.getByLabelText("Email"), "person@example.com")
    expect(screen.queryByText("Please enter a valid email")).toBeNull()
    const password = screen.getByLabelText("Password")
    expect(password.getAttribute("type")).toBe("password")
    await user.type(password, "Orchid!River!Lighthouse!9")
    await user.click(screen.getByRole("button", { name: "Sign in" }))
    await waitFor(() => expect(mocks.login).toHaveBeenCalledExactlyOnceWith({ email: "person@example.com", password: "Orchid!River!Lighthouse!9" }))
    expect(screen.getByRole("link", { name: "Sign up" }).getAttribute("href")).toBe("/signup")
})

it("shows the pending label and disables the button while signing in", () => {
    mocks.isLoginLoading = true
    render(<LoginPage />)
    const button = screen.getByRole("button", { name: "Signing in..." }) as HTMLButtonElement
    expect(button.disabled).toBe(true)
})

it("validates signup confirmation and excludes it from the account command", async () => {
    const user = userEvent.setup()
    render(<SignupPage />)
    await user.click(screen.getByRole("button", { name: "Create account" }))
    expect(await screen.findByText("Please confirm your password")).toBeTruthy()
    expect(mocks.signup).not.toHaveBeenCalled()
    await user.type(screen.getByLabelText("Email"), "person@example.com")
    expect(screen.queryByText("Please enter a valid email")).toBeNull()
    await user.type(screen.getByLabelText("Password", { exact: true }), "Orchid!River!Lighthouse!9")
    const confirmation = screen.getByLabelText("Confirm Password")
    expect(confirmation.getAttribute("type")).toBe("password")
    await user.type(confirmation, "different")
    await user.click(screen.getByRole("button", { name: "Create account" }))
    expect(await screen.findByText("Passwords do not match")).toBeTruthy()
    expect(mocks.signup).not.toHaveBeenCalled()
    await user.clear(confirmation)
    expect(screen.queryByText("Passwords do not match")).toBeNull()
    await user.type(confirmation, "Orchid!River!Lighthouse!9")
    await user.click(screen.getByRole("button", { name: "Create account" }))
    await waitFor(() => expect(mocks.signup).toHaveBeenCalledExactlyOnceWith({ email: "person@example.com", password: "Orchid!River!Lighthouse!9" }))
    expect(screen.getByRole("link", { name: "Sign in" }).getAttribute("href")).toBe("/login")
})

it("shows the pending label and disables the button while creating an account", () => {
    mocks.isSignupLoading = true
    render(<SignupPage />)
    const button = screen.getByRole("button", { name: "Creating account..." }) as HTMLButtonElement
    expect(button.disabled).toBe(true)
})
