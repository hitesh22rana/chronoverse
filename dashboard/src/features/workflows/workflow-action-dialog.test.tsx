// @vitest-environment jsdom
// Real dialog and form contracts for cancellation and pending actions.
import { cleanup, render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { afterEach, beforeEach, expect, it, vi } from "vitest"

import type { Workflow } from "./types"
import { WorkflowActionDialog } from "./workflow-action-dialog"

const workflow = {
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
} as Workflow

type DialogProps = Parameters<typeof WorkflowActionDialog>[0]

function renderDialog() {
    const onOpenChange = vi.fn()
    const onConfirm = vi.fn()
    const user = userEvent.setup()
    const props: DialogProps = {
        workflow,
        open: true,
        onOpenChange,
        onConfirm,
        isPending: false,
        title: "Terminate workflow",
        description: "This action will cancel all remaining jobs and scheduled executions for this workflow.",
        warning: "Terminating this workflow will terminate all ongoing jobs and prevent any future scheduled executions.",
        pendingLabel: "Terminating...",
    }
    const view = render(<WorkflowActionDialog {...props} />)
    return { ...view, props, user, onOpenChange, onConfirm }
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

beforeEach(installDomStubs)

afterEach(() => {
    cleanup()
    vi.unstubAllGlobals()
    restoreDomStubs()
})

it("closes without confirming when the destructive action is cancelled", async () => {
    const { user, onOpenChange, onConfirm } = renderDialog()

    await user.click(screen.getByRole("button", { name: "Cancel" }))

    expect(onOpenChange).toHaveBeenCalledWith(false)
    expect(onConfirm).not.toHaveBeenCalled()
})

it("shows the pending label and locks a valid form while the action is in flight", async () => {
    const { user, rerender, props, onConfirm, onOpenChange } = renderDialog()

    await user.type(screen.getByPlaceholderText("Enter workflow name"), workflow.name)
    await waitFor(() => expect(screen.getByRole("button", { name: "Terminate workflow" })).toHaveProperty("disabled", false))

    rerender(<WorkflowActionDialog {...props} isPending />)

    expect(screen.getByRole("button", { name: "Terminating..." })).toHaveProperty("disabled", true)
    expect(screen.getByPlaceholderText("Enter workflow name")).toHaveProperty("disabled", true)
    expect(screen.getByRole("button", { name: "Cancel" })).toHaveProperty("disabled", true)

    await user.click(screen.getByRole("button", { name: "Terminating..." }))
    await user.click(screen.getByRole("button", { name: "Cancel" }))

    expect(onConfirm).not.toHaveBeenCalled()
    expect(onOpenChange).not.toHaveBeenCalled()
})
