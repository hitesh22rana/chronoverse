// @vitest-environment jsdom

import { useForm } from "react-hook-form"
import { cleanup, render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { afterEach, expect, it, vi } from "vitest"
import { Form } from "@/components/ui/form"
import { WorkflowActionDialog } from "./workflow-action-dialog"
import type { Workflow } from "./types"
import { ContainerListField, WorkflowNumberField } from "./workflow-form-fields"

afterEach(cleanup)

it.each([
    ["cmd", "Command argument", "Add argument", "two"],
    ["env", "Environment variable", "Add variable", "TWO=2"],
] as const)("labels %s fields and keeps edited values paired with stable rows", async (name, label, addLabel, secondValue) => {
    const user = userEvent.setup()
    function Harness() {
        const form = useForm({ defaultValues: { containerPayload: {
            cmd: ["one", "two"], cmdIds: ["cmd-first", "cmd-second"],
            env: ["ONE=1", "TWO=2"], envIds: ["env-first", "env-second"],
        } } })
        return <Form {...form}><ContainerListField name={name}
            values={form.watch(`containerPayload.${name}`)}
            ids={form.watch(`containerPayload.${name}Ids`)} /></Form>
    }
    render(<Harness />)
    const first = screen.getByRole("textbox", { name: `${label} 1` })
    const second = screen.getByRole("textbox", { name: `${label} 2` })
    await user.type(second, "-edited")
    await user.click(screen.getByRole("button", { name: addLabel }))
    expect(screen.getAllByRole("textbox")).toHaveLength(3)
    expect(screen.getByRole("textbox", { name: `${label} 2` })).toBe(second)
    await user.click(screen.getByRole("button", { name: `Remove ${label.toLowerCase()} 1` }))
    expect(screen.getAllByRole("textbox")).toHaveLength(2)
    expect(screen.getByRole("textbox", { name: `${label} 1` })).toBe(second)
    expect((second as HTMLInputElement).value).toBe(`${secondValue}-edited`)
    expect(document.body.contains(first)).toBe(false)
    expect((screen.getByRole("textbox", { name: `${label} 2` }) as HTMLInputElement).value).toBe("")
})

it.each([
    ["heartbeatPayload.expectedStatusCode", 100, 599],
    ["interval", 1, undefined],
    ["maxConsecutiveJobFailuresAllowed", 3, undefined],
] as const)("submits %s as a number or an empty value within its input bounds", async (name, min, max) => {
    const user = userEvent.setup()
    const onSubmit = vi.fn()
    function Harness() {
        const form = useForm()
        return <Form {...form}><form noValidate onSubmit={form.handleSubmit(onSubmit)}>
            <WorkflowNumberField name={name} />
            <button type="submit">Save</button>
        </form></Form>
    }
    render(<Harness />)
    const input = screen.getByRole("spinbutton") as HTMLInputElement
    expect(input.min).toBe(String(min))
    expect(input.max).toBe(max === undefined ? "" : String(max))
    expect(input.value).toBe("")
    await user.type(input, "200")
    await user.clear(input)
    await user.click(screen.getByRole("button", { name: "Save" }))
    await waitFor(() => expect(onSubmit).toHaveBeenCalledOnce())
    const submittedValue = () => name.split(".").reduce((value, key) => value[key], onSubmit.mock.calls[0][0])
    expect(submittedValue()).toBe("")
    onSubmit.mockClear()
    await user.type(input, "200")
    await user.click(screen.getByRole("button", { name: "Save" }))
    await waitFor(() => expect(onSubmit).toHaveBeenCalledOnce())
    expect(submittedValue()).toBe(200)
})

it("requires the exact workflow name before confirming a destructive action", async () => {
    const user = userEvent.setup()
    const onConfirm = vi.fn()
    const onOpenChange = vi.fn()
    render(<WorkflowActionDialog workflow={{ name: "My workflow" } as Workflow} open
        onOpenChange={onOpenChange} onConfirm={onConfirm} isPending={false}
        title="Delete workflow" description="Delete" warning="Cannot be undone" pendingLabel="Deleting..." />)
    const input = screen.getByRole("textbox", { name: "To confirm, type My workflow" })
    const confirm = screen.getByRole("button", { name: "Delete workflow" }) as HTMLButtonElement
    await user.type(input, "wrong")
    expect(confirm.disabled).toBe(true)
    await user.click(confirm)
    expect(onConfirm).not.toHaveBeenCalled()
    expect(onOpenChange).not.toHaveBeenCalled()
    await user.clear(input)
    await user.type(input, "My workflow")
    await waitFor(() => expect(confirm.disabled).toBe(false))
    await user.click(confirm)
    await waitFor(() => expect(onConfirm).toHaveBeenCalledOnce())
    expect(onOpenChange).toHaveBeenCalledWith(false)
})
