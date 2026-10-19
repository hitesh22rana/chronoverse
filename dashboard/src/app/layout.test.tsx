// @vitest-environment jsdom
/**
 * The root layout runs under Next's compiled React, which exposes the
 * `ViewTransition` element that the installed `react` build does not, so a
 * pass-through stand-in is declared here. Fonts are fetched at build time by
 * `next/font`, so they are stubbed as well.
 */
import { renderToStaticMarkup } from "react-dom/server"
import type { ReactNode } from "react"
import { beforeEach, expect, it, vi } from "vitest"

vi.mock("next/font/google", () => ({
    Poppins: () => ({ variable: "--font-poppins" }),
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

it("wraps the app in the themed document shell", () => {
    const markup = renderToStaticMarkup(
        <RootLayout>
            <main>page content</main>
        </RootLayout>,
    )

    expect(metadata.title).toBe("Chronoverse - Distributed Task Scheduler & Orchestrator")
    expect(markup).toContain('lang="en"')
    expect(markup).toContain("--font-poppins")
    expect(markup).toContain("page content")
})
