// @vitest-environment jsdom
/**
 * Mounted tests for the create-workflow wizard: what each step validates before
 * it will advance, how the heartbeat and container configurations serialize into
 * the create request, and how a submission behaves while it is in flight, when
 * it succeeds and when it is rejected.
 *
 * Boundaries only: the transport (`@/lib/api/client`), the Next.js navigation
 * hooks and `sonner`. The dialog, react-hook-form, zod, react-query and the Radix
 * primitives all run for real, so the assertions are about what a user can see
 * and do, and about the request the form actually sends.
 */
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { afterEach, beforeEach, expect, it, vi } from "vitest"

import { CreateWorkflowDialog } from "./create-workflow-dialog"

const mocks = vi.hoisted(() => ({
    fetchApi: vi.fn(),
    fetchApiJson: vi.fn(),
    toast: { error: vi.fn(), success: vi.fn(), warning: vi.fn() },
    push: vi.fn(),
    replace: vi.fn(),
    back: vi.fn(),
    createCalls: [] as { url: string, init: RequestInit | undefined }[],
    holdCreate: false,
    releaseCreate: null as null | (() => void),
}))

vi.mock("next/navigation", () => ({
    usePathname: () => "/",
    useRouter: () => ({ push: mocks.push, replace: mocks.replace, back: mocks.back }),
    useSearchParams: () => new URLSearchParams(),
}))

vi.mock("sonner", () => ({ toast: mocks.toast, Toaster: () => null }))

vi.mock("@/lib/api/client", () => ({
    fetchApi: mocks.fetchApi,
    fetchApiJson: mocks.fetchApiJson,
    createIdempotencyKey: () => "idempotency-key",
}))

function renderDialog() {
    const queryClient = new QueryClient({
        defaultOptions: { queries: { retry: false, gcTime: 0 }, mutations: { retry: false } },
    })
    const user = userEvent.setup()
    const onOpenChange = vi.fn()
    const view = render(
        <QueryClientProvider client={queryClient}>
            <CreateWorkflowDialog open onOpenChange={onOpenChange} />
        </QueryClientProvider>,
    )
    return { ...view, user, onOpenChange }
}

const nextButton = () => screen.getByRole("button", { name: /Next|Create workflow|Creating/ })
const submitButton = () => screen.getByRole("button", { name: /Create workflow|Creating/ })
const previousButton = () => screen.queryByRole("button", { name: "Previous" })
const cancelButton = () => screen.queryByRole("button", { name: "Cancel" })
const nameField = () => screen.getByLabelText("Name") as HTMLInputElement
const kindSelect = () => screen.getByRole("combobox")
const endpointField = () => screen.getByLabelText("Endpoint URL") as HTMLInputElement
const statusCodeField = () => screen.getByLabelText("Expected Status Code") as HTMLInputElement
const heartbeatTimeoutField = () => screen.getByPlaceholderText("10s") as HTMLInputElement
const headerKeyField = () => screen.getByPlaceholderText("Header name") as HTMLInputElement
const headerValueField = () => screen.getByPlaceholderText("Value") as HTMLInputElement
const imageField = () => screen.getByLabelText("Image") as HTMLInputElement
const commandField = () => screen.getAllByPlaceholderText("sh -c 'echo hello'") as HTMLInputElement[]
const envField = () => screen.getAllByPlaceholderText("MY_ENV=VALUE") as HTMLInputElement[]
const containerTimeoutField = () => screen.getByPlaceholderText("30s") as HTMLInputElement
const intervalField = () => screen.getByLabelText("Interval (minutes)") as HTMLInputElement
const failuresField = () => screen.getByLabelText("Max consecutive failures allowed") as HTMLInputElement
const retainLogsSwitch = () => screen.getByRole("switch", { name: "Retain logs" })
const settingsGroup = () => screen.getByRole("group", { name: "Workflow settings" })
const currentStep = () => screen.getByText(/^Step \d of \d$/)
const stepHeading = () => screen.getByRole("heading", { level: 3 })

/** All three step panels stay mounted; the hidden one marks a later step. */
const shownNow = (field: HTMLElement) => expect(field.closest("[hidden]")).toBeNull()
const shownLater = (field: HTMLElement) => expect(field.closest("[hidden]")).not.toBeNull()

/** Applies a value the way a keystroke would, so zod's onChange mode runs. */
const type = (field: HTMLInputElement, value: string) =>
    act(() => {
        fireEvent.change(field, { target: { value } })
    })

/** Advances the wizard from wherever it stands to the given step (1-based). */
async function advanceToStep(user: ReturnType<typeof userEvent.setup>, target: 2 | 3) {
    for (let guard = 0; guard < 4; guard++) {
        const from = currentStep().textContent
        if (from === `Step ${target} of 3`) return
        await user.click(nextButton())
        await waitFor(() => expect(currentStep().textContent).not.toBe(from))
    }
    expect(currentStep().textContent).toBe(`Step ${target} of 3`)
}

async function chooseKind(user: ReturnType<typeof userEvent.setup>, option: string) {
    await user.click(kindSelect())
    await user.click(await screen.findByRole("option", { name: option }))
}

async function fillHeartbeat(user: ReturnType<typeof userEvent.setup>) {
    type(nameField(), "API heartbeat")
    await advanceToStep(user, 2)
    type(endpointField(), "https://example.com/health")
    type(statusCodeField(), "204")
    await user.click(screen.getByRole("button", { name: /Add header/ }))
    type(headerKeyField(), "X-Token")
    type(headerValueField(), "secret")
    type(heartbeatTimeoutField(), "10s")
    await advanceToStep(user, 3)
    type(intervalField(), "15")
    type(failuresField(), "4")
}

async function fillContainer(user: ReturnType<typeof userEvent.setup>) {
    type(nameField(), "Nightly container")
    await chooseKind(user, "Container")
    await advanceToStep(user, 2)
    type(imageField(), "alpine:latest")
    await user.click(screen.getByRole("button", { name: /Add argument/ }))
    type(commandField()[0], "sh")
    await user.click(screen.getByRole("button", { name: /Add variable/ }))
    type(envField()[0], "MODE=prod")
    type(containerTimeoutField(), "30s")
    await advanceToStep(user, 3)
    type(intervalField(), "15")
}

const recordedCreate = () => {
    const call = mocks.createCalls.at(-1)
    if (!call) throw new Error("no create request was sent")
    return call
}
const createBody = () => JSON.parse(String(recordedCreate().init?.body))
const formElement = () => document.querySelector("form") as HTMLFormElement

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
    if (!Element.prototype.scrollTo) {
        Element.prototype.scrollTo = () => {}
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
    mocks.createCalls = []
    mocks.holdCreate = false
    mocks.releaseCreate = null
    mocks.push.mockClear()
    mocks.back.mockClear()
    mocks.replace.mockClear()
    mocks.toast.error.mockClear()
    mocks.toast.success.mockClear()
    mocks.toast.warning.mockClear()
    mocks.fetchApi.mockReset().mockImplementation(async (url: string, _message: string, init?: RequestInit) => {
        mocks.createCalls.push({ url, init })
        if (mocks.holdCreate) {
            await new Promise<void>((resolve) => {
                mocks.releaseCreate = resolve
            })
        }
        return { ok: true, status: 201 }
    })
    mocks.fetchApiJson.mockReset().mockResolvedValue({ workflows: [] })
    installDomStubs()
})

afterEach(() => {
    cleanup()
    vi.unstubAllGlobals()
    restoreDomStubs()
})

it("walks the three steps, marking the current one and the completed ones", async () => {
    const { user } = renderDialog()

    expect(currentStep().textContent).toBe("Step 1 of 3")
    expect(stepHeading().textContent).toBe("Basics")
    expect(screen.getByText("Give your workflow a name and choose how it runs.")).toBeTruthy()
    expect(cancelButton()).toBeTruthy()
    expect(previousButton()).toBeNull()
    // Later steps stay mounted but are not shown yet.
    shownLater(endpointField())
    shownLater(intervalField())
    const progress = screen.getByRole("list", { name: "Workflow setup progress" })
    const stepStates = () =>
        within(progress).getAllByRole("listitem").map((item) => item.getAttribute("aria-current"))
    expect(stepStates()).toEqual(["step", null, null])

    type(nameField(), "API heartbeat")
    await user.click(nextButton())

    await waitFor(() => expect(stepHeading().textContent).toBe("Configuration"))
    expect(screen.getByText("Configure the HTTP request to monitor your service.")).toBeTruthy()
    expect(previousButton()).toBeTruthy()
    expect(cancelButton()).toBeNull()
    shownNow(endpointField())
    shownLater(nameField())
    // A step change moves the focus, so a screen reader lands on the new section.
    expect(document.activeElement).toBe(stepHeading())
    expect(stepStates()).toEqual([null, "step", null])
    expect(within(progress).getAllByText("Completed")).toHaveLength(1)

    await user.click(previousButton()!)

    await waitFor(() => expect(stepHeading().textContent).toBe("Basics"))
    expect(previousButton()).toBeNull()
    expect(cancelButton()).toBeTruthy()
    expect(stepStates()).toEqual(["step", null, null])
    expect(nameField()).toHaveProperty("value", "API heartbeat")
})

it("refuses to leave the first step without a usable name", async () => {
    const { user } = renderDialog()

    await user.click(nextButton())

    expect(await screen.findByText("Name must be at least 3 characters")).toBeTruthy()
    expect(currentStep().textContent).toBe("Step 1 of 3")
    expect(mocks.createCalls).toHaveLength(0)

    type(nameField(), "Nightly container")
    await waitFor(() => expect(screen.queryByText("Name must be at least 3 characters")).toBeNull())

    type(nameField(), "x".repeat(51))
    await user.click(nextButton())

    expect(await screen.findByText("Name must be at most 50 characters")).toBeTruthy()
    expect(currentStep().textContent).toBe("Step 1 of 3")
})

it("swaps the configuration and description when the kind changes", async () => {
    const { user } = renderDialog()

    expect(screen.getByText(/monitor the availability of your services/)).toBeTruthy()
    type(nameField(), "Nightly container")

    await chooseKind(user, "Container")

    expect(screen.getByText(/run custom code in a containerized environment/)).toBeTruthy()

    await advanceToStep(user, 2)

    expect(screen.getByText("Container execution")).toBeTruthy()
    expect(screen.getByText("Choose a container image and customize its execution.")).toBeTruthy()
    shownNow(imageField())
    // The heartbeat fields are not part of a container workflow at all.
    expect(screen.queryByLabelText("Endpoint URL")).toBeNull()

    await user.click(previousButton()!)
    await chooseKind(user, "Heartbeat")
    await advanceToStep(user, 2)

    expect(screen.getByText("HTTP request")).toBeTruthy()
    expect(screen.getByText("Configure the HTTP request to monitor your service.")).toBeTruthy()
    shownNow(endpointField())
    expect(screen.queryByLabelText("Image")).toBeNull()
})

it("keeps a rejected heartbeat configuration out of the request", async () => {
    const { user } = renderDialog()
    type(nameField(), "API heartbeat")

    await advanceToStep(user, 2)
    // An empty endpoint and a malformed one are both not a URL.
    await user.click(nextButton())
    expect(await screen.findByText("Invalid URL")).toBeTruthy()
    expect(currentStep().textContent).toBe("Step 2 of 3")
    expect(mocks.createCalls).toHaveLength(0)

    type(endpointField(), "not a url")
    await user.click(nextButton())
    expect(await screen.findByText("Invalid URL")).toBeTruthy()
    expect(currentStep().textContent).toBe("Step 2 of 3")

    // A header still needs its name before it can be sent.
    type(endpointField(), "https://example.com/health")
    await user.click(screen.getByRole("button", { name: /Add header/ }))
    await user.click(nextButton())

    expect(await screen.findByText("Header key is required")).toBeTruthy()
    expect(currentStep().textContent).toBe("Step 2 of 3")

    type(headerKeyField(), "X-Token")
    type(headerValueField(), "secret")
    await advanceToStep(user, 3)
    // A heartbeat emits no job logs, so it has no retention choice.
    expect(screen.queryByRole("switch", { name: "Retain logs" })).toBeNull()

    await user.click(nextButton())

    await waitFor(() => expect(mocks.createCalls).toHaveLength(1))
    expect(createBody()).toEqual({
        name: "API heartbeat",
        kind: "HEARTBEAT",
        payload: JSON.stringify({
            endpoint: "https://example.com/health",
            expected_status_code: 200,
            headers: { "X-Token": "secret" },
        }),
        interval: 5,
        max_consecutive_job_failures_allowed: 3,
        log_retention: false,
    })
})

it("refuses a heartbeat timeout longer than five minutes and an out-of-range status code", async () => {
    const { user } = renderDialog()
    type(nameField(), "API heartbeat")

    await advanceToStep(user, 2)
    type(endpointField(), "https://example.com/health")
    type(heartbeatTimeoutField(), "6m")

    await user.click(nextButton())

    expect(
        await screen.findByText(/Timeout must be a valid duration .e.g., '30s', '1m'. max up to 5 minutes/),
    ).toBeTruthy()
    expect(currentStep().textContent).toBe("Step 2 of 3")

    type(heartbeatTimeoutField(), "nonsense")
    await user.click(nextButton())
    expect(
        await screen.findByText(/Timeout must be a valid duration .e.g., '30s', '1m'. max up to 5 minutes/),
    ).toBeTruthy()

    type(heartbeatTimeoutField(), "30s")
    type(statusCodeField(), "99")

    await user.click(nextButton())

    expect(await screen.findByText(/expected number to be >=100/)).toBeTruthy()
    expect(currentStep().textContent).toBe("Step 2 of 3")

    type(statusCodeField(), "600")
    await user.click(nextButton())
    expect(await screen.findByText(/expected number to be <=599/)).toBeTruthy()
})

it("removes a heartbeat header again instead of sending a nameless one", async () => {
    const { user } = renderDialog()
    type(nameField(), "API heartbeat")

    await advanceToStep(user, 2)
    type(endpointField(), "https://example.com/health")
    await user.click(screen.getByRole("button", { name: /Add header/ }))
    type(headerKeyField(), "X-Token")
    type(headerValueField(), "secret")

    await user.click(screen.getByRole("button", { name: "Remove header 1" }))

    expect(screen.queryByPlaceholderText("Header name")).toBeNull()
    await advanceToStep(user, 3)
    await user.click(nextButton())

    await waitFor(() => expect(mocks.createCalls).toHaveLength(1))
    expect(JSON.parse(createBody().payload).headers).toEqual({})
})

it("keeps a rejected container configuration out of the request", async () => {
    const { user } = renderDialog()
    type(nameField(), "Nightly container")
    await chooseKind(user, "Container")

    await advanceToStep(user, 2)
    await user.click(nextButton())

    expect(await screen.findByText("Container image is required")).toBeTruthy()
    expect(currentStep().textContent).toBe("Step 2 of 3")

    type(imageField(), "alpine:latest")
    type(containerTimeoutField(), "2h")

    await user.click(nextButton())

    expect(
        await screen.findByText(/Timeout must be a valid duration .e.g., '30s', '5m'. max up to 1 hour/),
    ).toBeTruthy()
    expect(currentStep().textContent).toBe("Step 2 of 3")

    type(containerTimeoutField(), "5m")
    await advanceToStep(user, 3)
    await user.click(nextButton())

    await waitFor(() => expect(mocks.createCalls).toHaveLength(1))
    expect(createBody()).toMatchObject({
        kind: "CONTAINER",
        payload: JSON.stringify({ image: "alpine:latest", timeout: "5m" }),
    })
})

it("drops the command and variable rows it no longer needs", async () => {
    const { user } = renderDialog()
    type(nameField(), "Nightly container")
    await chooseKind(user, "Container")

    await advanceToStep(user, 2)
    type(imageField(), "alpine:latest")
    await user.click(screen.getByRole("button", { name: /Add argument/ }))
    await user.click(screen.getByRole("button", { name: /Add argument/ }))
    expect(commandField()).toHaveLength(2)
    type(commandField()[0], "sh")
    type(commandField()[1], "-c")

    await user.click(screen.getByRole("button", { name: "Remove command argument 2" }))

    expect(commandField()).toHaveLength(1)

    await user.click(screen.getByRole("button", { name: /Add variable/ }))
    type(envField()[0], "MODE=prod")

    await advanceToStep(user, 3)
    await user.click(nextButton())

    await waitFor(() => expect(mocks.createCalls).toHaveLength(1))
    expect(JSON.parse(createBody().payload)).toEqual({
        image: "alpine:latest",
        cmd: ["sh"],
        env: { MODE: "prod" },
    })
})

it("refuses to create from the schedule step until the schedule is usable", async () => {
    const { user } = renderDialog()
    type(nameField(), "API heartbeat")

    await advanceToStep(user, 2)
    type(endpointField(), "https://example.com/health")
    await advanceToStep(user, 3)
    type(intervalField(), "0")

    await user.click(nextButton())

    expect(await screen.findByText("Must be a whole number between 1 and 10080 minutes (1 week)")).toBeTruthy()
    expect(currentStep().textContent).toBe("Step 3 of 3")
    expect(mocks.createCalls).toHaveLength(0)

    type(intervalField(), "15")
    await waitFor(() =>
        expect(screen.queryByText("Must be a whole number between 1 and 10080 minutes (1 week)")).toBeNull(),
    )

    type(intervalField(), "1.5")
    await user.click(nextButton())
    expect(await screen.findByText("Must be a whole number between 1 and 10080 minutes (1 week)")).toBeTruthy()
    expect(mocks.createCalls).toHaveLength(0)

    type(intervalField(), "10081")
    await user.click(nextButton())
    expect(await screen.findByText("Must be a whole number between 1 and 10080 minutes (1 week)")).toBeTruthy()

    // Two allowed failures would never stop a broken workflow, so the form is
    // left short of a usable schedule rather than creating it.
    type(intervalField(), "15")
    type(failuresField(), "2")
    await user.click(nextButton())

    expect(await screen.findByText(/expected number to be >=3/)).toBeTruthy()
    expect(currentStep().textContent).toBe("Step 3 of 3")
    expect(mocks.createCalls).toHaveLength(0)

    // Repairing the last complaint makes the same request succeed.
    type(failuresField(), "3")
    await user.click(nextButton())

    await waitFor(() => expect(mocks.createCalls).toHaveLength(1))
    expect(createBody()).toMatchObject({ interval: 15, max_consecutive_job_failures_allowed: 3 })
})

it("sends the heartbeat request the API expects and closes on success", async () => {
    mocks.holdCreate = true
    const { user, onOpenChange } = renderDialog()

    await fillHeartbeat(user)
    await user.click(submitButton())

    await waitFor(() => expect(mocks.createCalls).toHaveLength(1))
    const { url, init } = recordedCreate()
    expect(url).toBe("/workflows")
    expect(init?.method).toBe("POST")
    expect((init?.headers as Record<string, string>)["Idempotency-Key"]).toBe("idempotency-key")
    expect(createBody()).toEqual({
        name: "API heartbeat",
        kind: "HEARTBEAT",
        payload: JSON.stringify({
            endpoint: "https://example.com/health",
            expected_status_code: 204,
            headers: { "X-Token": "secret" },
            timeout: "10s",
        }),
        interval: 15,
        max_consecutive_job_failures_allowed: 4,
        // A heartbeat emits no job logs, so retention is not its choice.
        log_retention: false,
    })
    expect(onOpenChange).not.toHaveBeenCalled()

    await act(async () => {
        mocks.releaseCreate?.()
    })

    await waitFor(() => expect(onOpenChange).toHaveBeenCalledWith(false))
    expect(mocks.toast.success).toHaveBeenCalledWith("workflow created successfully")
})

it("sends the container request the API expects, honouring the log-retention switch", async () => {
    const { user } = renderDialog()

    await fillContainer(user)
    // Only a container workflow keeps logs, so the switch belongs to its step.
    expect(retainLogsSwitch().getAttribute("data-state")).toBe("checked")

    await user.click(retainLogsSwitch())
    await user.click(submitButton())

    await waitFor(() => expect(mocks.createCalls).toHaveLength(1))
    expect(createBody()).toEqual({
        name: "Nightly container",
        kind: "CONTAINER",
        payload: JSON.stringify({
            image: "alpine:latest",
            cmd: ["sh"],
            env: { MODE: "prod" },
            timeout: "30s",
        }),
        interval: 15,
        max_consecutive_job_failures_allowed: 3,
        log_retention: false,
    })
})

it("keeps log retention on by default for a container workflow", async () => {
    const { user } = renderDialog()

    await fillContainer(user)
    await user.click(submitButton())

    await waitFor(() => expect(mocks.createCalls).toHaveLength(1))
    expect(createBody()).toMatchObject({ kind: "CONTAINER", log_retention: true })
})

it("locks the dialog while the create is in flight", async () => {
    mocks.holdCreate = true
    const { user, onOpenChange } = renderDialog()

    await fillContainer(user)
    await user.click(submitButton())

    await waitFor(() => expect(mocks.createCalls).toHaveLength(1))
    expect(await screen.findByRole("button", { name: /Creating/ })).toHaveProperty("disabled", true)
    expect(previousButton()).toHaveProperty("disabled", true)
    expect(settingsGroup()).toHaveProperty("disabled", true)
    expect(screen.getByText("Creating…")).toBeTruthy()

    // Neither the escape key nor a resubmit may abandon or duplicate a create.
    await user.keyboard("{Escape}")
    expect(onOpenChange).not.toHaveBeenCalled()
    act(() => {
        fireEvent.submit(formElement())
    })
    expect(mocks.createCalls).toHaveLength(1)
    expect(onOpenChange).not.toHaveBeenCalled()

    await act(async () => {
        mocks.releaseCreate?.()
    })

    await waitFor(() => expect(onOpenChange).toHaveBeenCalledWith(false))

    // The same submission does go through once the create settles, so the single
    // request above came from the in-flight guard rather than a dead form.
    mocks.holdCreate = false
    act(() => {
        fireEvent.submit(formElement())
    })

    await waitFor(() => expect(mocks.createCalls).toHaveLength(2))
})

it("keeps the dialog and its values when the create is rejected, and retries on request", async () => {
    mocks.fetchApi.mockImplementation(async (url: string, _message: string, init?: RequestInit) => {
        mocks.createCalls.push({ url, init })
        if (mocks.createCalls.length === 1) {
            throw new Error("failed to create workflow: 422 invalid")
        }
        return { ok: true, status: 201 }
    })
    const { user, onOpenChange } = renderDialog()

    await fillHeartbeat(user)
    await user.click(submitButton())

    await waitFor(() =>
        expect(mocks.toast.error).toHaveBeenCalledWith("failed to create workflow: 422 invalid"),
    )
    expect(onOpenChange).not.toHaveBeenCalled()
    // Everything the user typed is still on screen for another attempt.
    expect(currentStep().textContent).toBe("Step 3 of 3")
    expect(intervalField()).toHaveProperty("value", "15")
    expect(failuresField()).toHaveProperty("value", "4")
    expect(settingsGroup()).toHaveProperty("disabled", false)
    expect(screen.getByRole("button", { name: "Create workflow" })).toHaveProperty("disabled", false)

    await user.click(screen.getByRole("button", { name: "Create workflow" }))

    await waitFor(() => expect(mocks.createCalls).toHaveLength(2))
    expect(JSON.parse(String(mocks.createCalls[1].init?.body))).toEqual(createBody())
    await waitFor(() => expect(onOpenChange).toHaveBeenCalledWith(false))
    expect(mocks.toast.success).toHaveBeenCalledWith("workflow created successfully")
})

it("closes without creating when the first step is cancelled", async () => {
    const { user, onOpenChange } = renderDialog()

    await user.click(cancelButton()!)

    expect(onOpenChange).toHaveBeenCalledWith(false)
    expect(mocks.createCalls).toHaveLength(0)
})

it("closes on escape once the dialog is idle", async () => {
    const { user, onOpenChange } = renderDialog()

    await user.keyboard("{Escape}")

    expect(onOpenChange).toHaveBeenCalledWith(false)
    expect(mocks.createCalls).toHaveLength(0)
})

it("renders nothing and fetches nothing while the dialog is closed", () => {
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(
        <QueryClientProvider client={queryClient}>
            <CreateWorkflowDialog open={false} onOpenChange={vi.fn()} />
        </QueryClientProvider>,
    )
    expect(screen.queryByText("Create new workflow")).toBeNull()
    expect(mocks.fetchApiJson).not.toHaveBeenCalled()
})
