// @vitest-environment jsdom

import { createRef } from "react"
import { cleanup, render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { useForm } from "react-hook-form"
import { afterEach, expect, it, vi } from "vitest"

import { Badge } from "./badge"
import { Button } from "./button"
import { Form, FormControl, FormDescription, FormField, FormItem, FormLabel, FormMessage } from "./form"

afterEach(() => {
    cleanup()
    vi.restoreAllMocks()
})

it("forwards badge and button props, refs and interaction through their child", async () => {
    const user = userEvent.setup()
    const badgeRef = createRef<HTMLSpanElement>()
    const buttonRef = createRef<HTMLButtonElement>()
    const onClick = vi.fn()
    render(<>
        <Badge asChild ref={badgeRef} className="custom-badge" title="Live status"><span>Running</span></Badge>
        <Button asChild ref={buttonRef} className="custom-button" onClick={onClick}><button type="button">Run job</button></Button>
    </>)
    const badge = screen.getByText("Running")
    const button = screen.getByRole("button", { name: "Run job" })
    expect(badgeRef.current).toBe(badge)
    expect(badge.getAttribute("data-slot")).toBe("badge")
    expect(badge.classList.contains("custom-badge")).toBe(true)
    expect(badge.title).toBe("Live status")
    expect(buttonRef.current).toBe(button)
    expect(button.getAttribute("data-slot")).toBe("button")
    expect(button.classList.contains("custom-button")).toBe(true)
    await user.click(button)
    expect(onClick).toHaveBeenCalledTimes(1)
})

function FieldHarness({ errorMessage, withMessage = true }: { errorMessage?: string; withMessage?: boolean }) {
    const form = useForm({ defaultValues: { name: "" } })
    return <Form {...form}>
        <FormField control={form.control} name="name" render={({ field }) => <FormItem>
            <FormLabel>Name</FormLabel>
            <FormControl><input {...field} /></FormControl>
            <FormDescription>Use a unique name.</FormDescription>
            <FormMessage>{withMessage ? "Ready" : undefined}</FormMessage>
        </FormItem>} />
        <button type="button" onClick={() => form.setError("name", { type: "server", message: errorMessage })}>Reject name</button>
        <button type="button" onClick={() => form.clearErrors("name")}>Clear error</button>
    </Form>
}

it("pairs label, help and error IDs and restores the normal field after clearing errors", async () => {
    const user = userEvent.setup()
    render(<FieldHarness errorMessage="Name already exists" />)
    const input = screen.getByRole("textbox", { name: "Name" })
    const description = screen.getByText("Use a unique name.")
    expect(input.getAttribute("aria-describedby")).toBe(description.id)
    expect(input.getAttribute("aria-invalid")).toBe("false")
    expect(screen.getByText("Ready").id).toBe(`${input.id.replace(/-form-item$/, "")}-form-item-message`)
    await user.click(screen.getByRole("button", { name: "Reject name" }))
    const error = await screen.findByText("Name already exists")
    expect(input.getAttribute("aria-invalid")).toBe("true")
    expect(input.getAttribute("aria-describedby")).toBe(`${description.id} ${error.id}`)
    expect(screen.queryByText("Ready")).toBeNull()
    await user.click(screen.getByRole("button", { name: "Clear error" }))
    await waitFor(() => expect(input.getAttribute("aria-invalid")).toBe("false"))
    expect(input.getAttribute("aria-describedby")).toBe(description.id)
    expect(screen.queryByText("Name already exists")).toBeNull()
    expect(screen.getByText("Ready")).toBeDefined()
})

it("does not show a fabricated message for a real server error without message text", async () => {
    const user = userEvent.setup()
    const { container } = render(<FieldHarness withMessage={false} />)
    expect(container.querySelector('[data-slot="form-message"]')).toBeNull()
    await user.click(screen.getByRole("button", { name: "Reject name" }))
    await waitFor(() => expect(screen.getByRole("textbox").getAttribute("aria-invalid")).toBe("true"))
    expect(container.querySelector('[data-slot="form-message"]')).toBeNull()
})

it("reports a missing field provider before trying to read a nonexistent field name", () => {
    function MissingField() {
        const form = useForm({ defaultValues: { name: "" } })
        return <Form {...form}><FormItem><FormLabel>Name</FormLabel></FormItem></Form>
    }
    vi.spyOn(console, "error").mockImplementation(() => {})
    expect(() => render(<MissingField />)).toThrow("useFormField should be used within <FormField>")
})

it("reports a missing item provider instead of producing undefined accessible IDs", () => {
    function MissingItem() {
        const form = useForm({ defaultValues: { name: "" } })
        return <Form {...form}><FormField control={form.control} name="name" render={() => <FormLabel>Name</FormLabel>} /></Form>
    }
    vi.spyOn(console, "error").mockImplementation(() => {})
    expect(() => render(<MissingItem />)).toThrow("useFormField should be used within <FormItem>")
})
