// @vitest-environment jsdom
import { cleanup, render, screen } from "@testing-library/react"
import { afterEach, beforeEach, expect, it, vi } from "vitest"

import { ThemeProvider } from "@/components/theme-provider"

beforeEach(() => {
    // next-themes reads the media query even when the system theme is unused.
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
})

afterEach(() => {
    cleanup()
    vi.unstubAllGlobals()
})

it("applies the configured attribute and renders its children", () => {
    render(
        <ThemeProvider attribute="class" defaultTheme="dark">
            <span>themed page</span>
        </ThemeProvider>,
    )

    expect(screen.getByText("themed page")).toBeTruthy()
    expect(document.documentElement.classList.contains("dark")).toBe(true)
})
