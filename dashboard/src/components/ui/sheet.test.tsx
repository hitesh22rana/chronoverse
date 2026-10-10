// @vitest-environment jsdom
import { useState } from "react"
import { cleanup, fireEvent, render, screen } from "@testing-library/react"
import { afterEach, expect, it, vi } from "vitest"

import { Sheet, SheetContent, SheetFooter, SheetHeader, SheetTitle } from "@/components/ui/sheet"

afterEach(cleanup)

it("opens in a portal and closes through its accessible close button", () => {
    const onOpenChange = vi.fn()
    function Example() {
        const [open, setOpen] = useState(false)
        return (
            <>
                <button onClick={() => setOpen(true)}>Show details</button>
                <Sheet open={open} onOpenChange={(value) => {
                    setOpen(value)
                    onOpenChange(value)
                }}>
                    <SheetContent aria-describedby={undefined}>
                        <SheetHeader><SheetTitle>Details</SheetTitle></SheetHeader>
                        <SheetFooter>Footer actions</SheetFooter>
                    </SheetContent>
                </Sheet>
            </>
        )
    }
    const { container } = render(<Example />)
    expect(screen.queryByRole("dialog")).toBeNull()

    fireEvent.click(screen.getByRole("button", { name: "Show details" }))

    const dialog = screen.getByRole("dialog", { name: "Details" })
    expect(container.contains(dialog)).toBe(false)
    expect(document.querySelector('[data-slot="sheet-overlay"]')?.getAttribute("data-state")).toBe("open")
    expect(dialog.className).toContain("right-0")
    expect(screen.getByText("Footer actions").getAttribute("data-slot")).toBe("sheet-footer")

    fireEvent.click(screen.getByRole("button", { name: "Close" }))

    expect(onOpenChange).toHaveBeenCalledWith(false)
    expect(screen.queryByRole("dialog")).toBeNull()
    expect(document.querySelector('[data-slot="sheet-overlay"]')).toBeNull()
})

it.each([
    ["left", "left-0", "border-r"],
    ["right", "right-0", "border-l"],
    ["top", "top-0", "border-b"],
    ["bottom", "bottom-0", "border-t"],
] as const)("positions the sheet on the %s side and forwards attributes and classes", (side, position, border) => {
    render(
        <Sheet defaultOpen>
            <SheetContent side={side} className="custom-content" data-testid="details" aria-describedby={undefined}>
                <SheetHeader className="custom-header" data-testid="header">
                    <SheetTitle className="custom-title" data-testid="title">Details</SheetTitle>
                </SheetHeader>
                <SheetFooter className="custom-footer" data-testid="footer">Actions</SheetFooter>
            </SheetContent>
        </Sheet>,
    )

    const dialog = screen.getByRole("dialog", { name: "Details" })
    expect(dialog).toBe(screen.getByTestId("details"))
    expect(dialog.className.split(" ")).toEqual(expect.arrayContaining([position, border, "custom-content"]))
    expect(dialog.className).toContain(`slide-in-from-${side}`)
    expect(screen.getByTestId("header").className).toContain("custom-header")
    expect(screen.getByTestId("title").className).toContain("custom-title")
    expect(screen.getByTestId("footer").className).toContain("custom-footer")
})

it("notifies a controlled owner when Escape dismisses the sheet", () => {
    const onOpenChange = vi.fn()
    render(
        <Sheet open onOpenChange={onOpenChange}>
            <SheetContent aria-describedby={undefined}><SheetTitle>Details</SheetTitle></SheetContent>
        </Sheet>,
    )

    fireEvent.keyDown(screen.getByRole("dialog"), { key: "Escape" })

    expect(onOpenChange).toHaveBeenCalledOnce()
    expect(onOpenChange).toHaveBeenCalledWith(false)
})
