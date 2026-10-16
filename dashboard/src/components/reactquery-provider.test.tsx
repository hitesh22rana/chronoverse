// @vitest-environment jsdom
import { cleanup, render, screen } from "@testing-library/react"
import { useQueryClient, type QueryClient } from "@tanstack/react-query"
import { afterEach, expect, it } from "vitest"
import { ReactQueryProvider } from "./reactquery-provider"

afterEach(cleanup)

it("preserves the client and cached data across rerenders while isolating mounts", () => {
    const clients: QueryClient[] = []
    function Consumer({ label }: { label: string }) {
        const client = useQueryClient()
        clients.push(client)
        return <span>{label}: {String(client.getQueryData(["account"]) ?? "empty")}</span>
    }
    const view = render(<ReactQueryProvider><Consumer label="first" /></ReactQueryProvider>)
    const original = clients[0]
    expect(original.getDefaultOptions().queries?.refetchOnWindowFocus).toBe(false)
    original.setQueryData(["account"], "cached")
    view.rerender(<ReactQueryProvider><Consumer label="rerender" /></ReactQueryProvider>)
    expect(clients.at(-1)).toBe(original)
    expect(screen.getByText("rerender: cached")).toBeTruthy()
    view.unmount()
    render(<ReactQueryProvider><Consumer label="new mount" /></ReactQueryProvider>)
    expect(clients.at(-1)).not.toBe(original)
    expect(screen.getByText("new mount: empty")).toBeTruthy()
    clients.forEach((client) => client.clear())
})
