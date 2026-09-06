import { Children, isValidElement, type ReactElement, type ReactNode } from "react"
import { renderToStaticMarkup } from "react-dom/server"
import * as rhf from "react-hook-form"
import { afterEach, expect, it, vi } from "vitest"
import { Form } from "@/components/ui/form"
import { Input } from "@/components/ui/input"
import { Button } from "@/components/ui/button"
import { WorkflowActionDialog } from "./workflow-action-dialog"
import type { Workflow } from "./types"
import { ContainerListField, WorkflowNumberField } from "./workflow-form-fields"

vi.mock("react-hook-form", async (importOriginal) => {
    const actual = await importOriginal<typeof rhf>()
    return { ...actual, useForm: vi.fn(actual.useForm), useFormContext: vi.fn(actual.useFormContext) }
})

function descendants(node: ReactNode): ReactElement<any>[] {
    return Children.toArray(node).flatMap((child) => isValidElement<{ children?: ReactNode }>(child)
        ? [child, ...descendants(child.props.children)] : [])
}

function renderForm(children: ReactNode) {
    let form!: rhf.UseFormReturn
    function Harness() {
        form = rhf.useForm<rhf.FieldValues>({ defaultValues: { containerPayload: { cmd: ["one", "two"], env: ["A=1", "B=2"] } } })
        return <Form {...form}>{children}</Form>
    }
    const html = renderToStaticMarkup(<Harness />)
    vi.mocked(rhf.useFormContext).mockReturnValue(form)
    return { form, html }
}

afterEach(() => { vi.restoreAllMocks(); vi.resetAllMocks() })

it.each(["cmd", "env"] as const)("labels %s fields and keeps values paired with stable IDs", (name) => {
    const props = { name, values: ["one", "two"], ids: ["first", "second"] }
    const { form, html } = renderForm(<ContainerListField {...props} />)
    for (const input of html.matchAll(/<input[^>]*\sid="([^"]+)"/g)) {
        expect(html).toContain(`for="${input[1]}"`)
    }
    expect(html.match(/<input/g)).toHaveLength(2)
    const buttons = descendants(ContainerListField(props)).filter((node) => node.type === Button)
    const setValue = vi.spyOn(form, "setValue")
    buttons[0].props.onClick()
    expect(setValue).toHaveBeenCalledWith(`containerPayload.${name}`, ["one", "two", ""])
    expect(setValue).toHaveBeenCalledWith(`containerPayload.${name}Ids`, ["first", "second", expect.any(String)])
    setValue.mockClear()
    buttons[1].props.onClick()
    expect(setValue).toHaveBeenCalledWith(`containerPayload.${name}`, ["two"])
    expect(setValue).toHaveBeenCalledWith(`containerPayload.${name}Ids`, ["second"])
})

it.each([
    ["heartbeatPayload.expectedStatusCode", 100, 599],
    ["interval", 1, undefined],
    ["maxConsecutiveJobFailuresAllowed", 3, undefined],
] as const)("preserves %s numeric input bounds and empty values", (name, min, max) => {
    renderForm(<WorkflowNumberField name={name} />)
    const onChange = vi.fn()
    const tree = WorkflowNumberField({ name }).props.render({ field: { value: undefined, onChange } })
    const input = descendants(tree).find((node) => node.type === Input)!
    expect(input.props).toMatchObject({ min, max, value: "", type: "number" })
    input.props.onChange({ target: { value: "" } })
    input.props.onChange({ target: { value: "200" } })
    expect(onChange.mock.calls).toEqual([[""], [200]])
})

it("requires the exact workflow name before confirming a destructive action", async () => {
    const onConfirm = vi.fn()
    const onOpenChange = vi.fn()
    let tree!: ReactElement
    function Harness() {
        tree = WorkflowActionDialog({ workflow: { name: "My workflow" } as Workflow, open: true, onOpenChange, onConfirm, isPending: false, title: "Delete workflow", description: "Delete", warning: "Cannot be undone", pendingLabel: "Deleting..." })
        return null
    }
    renderToStaticMarkup(<Harness />)
    const form = vi.mocked(rhf.useForm).mock.results.at(-1)!.value as rhf.UseFormReturn
    const submit = descendants(tree).find((node) => node.type === "form")!.props.onSubmit
    form.setValue("confirmName", "wrong")
    await submit()
    expect(onConfirm).not.toHaveBeenCalled()
    expect(onOpenChange).not.toHaveBeenCalled()
    form.setValue("confirmName", "My workflow")
    await submit()
    expect(onConfirm).toHaveBeenCalledOnce()
    expect(onOpenChange).toHaveBeenCalledWith(false)
})
