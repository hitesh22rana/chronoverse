// @vitest-environment jsdom
/**
 * The root layout runs under Next's compiled React, which exposes the
 * `ViewTransition` element that the installed `react` build does not, so a
 * pass-through stand-in is declared here. Fonts are fetched at build time by
 * `next/font`, so they are stubbed as well.
 */
import { renderToStaticMarkup } from "react-dom/server"
import { useQueryClient } from "@tanstack/react-query"
import { useTheme } from "next-themes"
import type { ReactNode } from "react"
import { afterEach, beforeEach, expect, it, vi } from "vitest"

vi.mock("next/font/google", () => ({
    Poppins: ({ variable }: { variable: string }) => ({ variable }),
}))

// The stylesheet needs the app's PostCSS pipeline, which the test transform
// does not run; layout only imports it for its side effect.
vi.mock("@/app/globals.css", () => ({}))

vi.mock("react", async (importOriginal) => ({
    ...(await importOriginal<typeof import("react")>()),
    ViewTransition: ({ children }: { children: ReactNode }) => children,
}))

import RootLayout, { metadata } from "@/app/layout"

beforeEach(() => {
    // next-themes reads the media query while resolving the system theme.
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

afterEach(() => vi.unstubAllGlobals())

it("wraps the app in the themed document shell", () => {
    function Page() {
        useQueryClient()
        const { theme } = useTheme()
        return <main data-theme={theme}>page content</main>
    }
    const markup = renderToStaticMarkup(
        <RootLayout>
            <Page />
        </RootLayout>,
    )
    const document = new DOMParser().parseFromString(markup, "text/html")

    expect(metadata.title).toBe("Chronoverse - Distributed Task Scheduler & Orchestrator")
    expect(document.documentElement.lang).toBe("en")
    expect(document.body.classList.contains("--font-sans")).toBe(true)
    expect(document.body.classList.contains("--font-heading")).toBe(true)
    expect(document.querySelector("main")?.textContent).toBe("page content")
    expect(document.querySelector("main")?.getAttribute("data-theme")).toBe("system")
})
