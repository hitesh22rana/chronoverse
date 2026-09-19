import { Children, isValidElement, useState, type ReactElement, type ReactNode } from "react"
import { renderToStaticMarkup } from "react-dom/server"
import * as rhf from "react-hook-form"
import { afterEach, expect, it, vi } from "vitest"
import { Button } from "@/components/ui/button"
import { Dialog, DialogFooter } from "@/components/ui/dialog"
import { FormField } from "@/components/ui/form"
import { Select } from "@/components/ui/select"
import { CreateWorkflowDialog } from "./create-workflow-dialog"
import { useWorkflows } from "./use-workflows"

vi.mock("react", async (original) => {
    const actual = await original<typeof import("react")>()
    return { ...actual, useState: vi.fn(actual.useState) }
})
vi.mock("react-hook-form", async (original) => {
    const actual = await original<typeof rhf>()
    return { ...actual, useForm: vi.fn(actual.useForm), useWatch: vi.fn(actual.useWatch) }
})
vi.mock("./use-workflows", () => ({ useWorkflows: vi.fn() }))

function descendants(node: ReactNode): ReactElement<any>[] {
    return Children.toArray(node).flatMap((child) => isValidElement<{ children?: ReactNode }>(child)
        ? [child, ...descendants(child.props.children)] : [])
}

async function setup() {
    const actual = await vi.importActual<typeof rhf>("react-hook-form")
    const createWorkflow = vi.fn()
    const onOpenChange = vi.fn()
    const setStep = vi.fn()
    let form: rhf.UseFormReturn
    let tree: ReactElement
    vi.mocked(useWorkflows).mockReturnValue({ createWorkflow, isCreating: false } as unknown as ReturnType<typeof useWorkflows>)
    vi.mocked(rhf.useForm).mockImplementation((options) => {
        form ??= actual.useForm(options) as rhf.UseFormReturn
        form.control._state.mount = true
        return form
    })
    vi.mocked(rhf.useWatch).mockImplementation((options?: { name?: string }) => form.getValues(options?.name ?? ""))
    function render(step: number) {
        vi.mocked(useState).mockReturnValueOnce([step, setStep])
        function Harness() {
            const element = CreateWorkflowDialog({ open: true, onOpenChange })!
            tree = (element.type as (_props: typeof element.props) => ReactElement)(element.props)
            return null
        }
        renderToStaticMarkup(<Harness />)
        const nodes = descendants(tree!)
        return {
            submit: () => nodes.find((node) => node.type === "form")!.props.onSubmit({ preventDefault: vi.fn(), persist: vi.fn() }),
            previous: () => nodes.find((node) => node.type === Button && descendants(node.props.children).some((child) => child.props["data-icon"] === "inline-start"))!.props.onClick(),
            changeKind: (kind: string) => {
                const field = nodes.find((node) => node.type === FormField && node.props.name === "kind")!
                const select = descendants(field.props.render({ field: { value: form.getValues("kind") } })).find((node) => node.type === Select)!
                select.props.onValueChange(kind)
            },
            nodes,
        }
    }
    render(0)
    return { render, form: form!, createWorkflow, onOpenChange, setStep }
}

afterEach(() => { vi.clearAllMocks() })

it("validates each step without submitting early and preserves the draft on Previous", async () => {
    const { render, form, createWorkflow, setStep } = await setup()
    await render(0).submit()
    expect(setStep).not.toHaveBeenCalled()
    form.setValue("name", "Health check")
    await render(0).submit()
    expect(setStep).toHaveBeenLastCalledWith(1)
    setStep.mockClear()
    await render(1).submit()
    expect(setStep).not.toHaveBeenCalled()
    form.setValue("heartbeatPayload.endpoint", "https://example.com/health")
    form.setValue("heartbeatPayload.headers", [{ id: "one", key: "X-Test", value: "yes" }])
    await render(1).submit()
    expect(setStep).toHaveBeenLastCalledWith(2)
    expect(createWorkflow).not.toHaveBeenCalled()
    render(2).previous()
    expect(setStep).toHaveBeenLastCalledWith(1)
    expect(form.getValues("heartbeatPayload.headers")).toEqual([{ id: "one", key: "X-Test", value: "yes" }])
})

it.each(["HEARTBEAT", "CONTAINER"])("submits %s only after valid scheduling, closing only on success", async (kind) => {
    const { render, form, createWorkflow, onOpenChange } = await setup()
    form.setValue("name", "My workflow")
    render(0).changeKind(kind)
    form.setValue("heartbeatPayload.endpoint", "https://example.com/health")
    form.setValue("containerPayload.image", "alpine:latest")
    form.setValue("interval", 0)
    await render(2).submit()
    expect(createWorkflow).not.toHaveBeenCalled()
    form.setValue("interval", 5)
    await render(2).submit()
    expect(createWorkflow).toHaveBeenCalledWith(expect.objectContaining({ kind, interval: 5, log_retention: kind === "CONTAINER" }), expect.any(Function))
    expect(onOpenChange).not.toHaveBeenCalled()
    expect(form.getValues("name")).toBe("My workflow")
    // A failed request has no success callback; the draft remains available to retry.
    await render(2).submit()
    expect(createWorkflow).toHaveBeenCalledTimes(2)
    createWorkflow.mock.calls[1][1]()
    expect(onOpenChange).toHaveBeenCalledWith(false)
})

it("preserves each kind's configuration while validating only the selected kind", async () => {
    const { render, form, setStep } = await setup()
    form.setValue("heartbeatPayload.endpoint", "https://example.com/health")
    render(0).changeKind("CONTAINER")
    await render(1).submit()
    expect(setStep).not.toHaveBeenCalled()
    form.setValue("containerPayload.image", "alpine:latest")
    form.setValue("containerPayload.cmd", ["echo", "hello"])
    await render(1).submit()
    expect(setStep).toHaveBeenLastCalledWith(2)
    render(0).changeKind("HEARTBEAT")
    expect(form.getValues("heartbeatPayload.endpoint")).toBe("https://example.com/health")
    render(0).changeKind("CONTAINER")
    expect(form.getValues("containerPayload.cmd")).toEqual(["echo", "hello"])
})

it("blocks submission and closing while pending, and unmounts closed drafts", async () => {
    const { render, createWorkflow, onOpenChange } = await setup()
    vi.mocked(useWorkflows).mockReturnValue({ createWorkflow, isCreating: true } as unknown as ReturnType<typeof useWorkflows>)
    const { nodes, submit } = render(2)
    await submit()
    nodes.find((node) => node.type === Dialog)!.props.onOpenChange(false)
    expect(createWorkflow).not.toHaveBeenCalled()
    expect(onOpenChange).not.toHaveBeenCalled()
    expect(descendants(nodes.find((node) => node.type === DialogFooter)).filter((node) => node.type === Button).every((node) => node.props.disabled)).toBe(true)
    expect(CreateWorkflowDialog({ open: false, onOpenChange })).toBeNull()
})
