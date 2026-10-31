import { afterEach, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { SWRConfig } from "swr";
import { SearchProvider, SearchTrigger } from "@/components/docs/search-dialog";

afterEach(() => { cleanup(); vi.unstubAllGlobals(); });

it("loads the actual lazy search panel, fetches the index and closes the dialog", async () => {
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue({ ok: true, json: async () => [{ title: "Replay safety", description: "Reliable retries", headings: "", href: "/docs/engineering/replay-safety", section: "Engineering", text: "replay" }] }));
  render(<SWRConfig value={{ provider: () => new Map() }}><SearchProvider><SearchTrigger /></SearchProvider></SWRConfig>);
  expect(screen.queryByRole("dialog")).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Search documentation" }));
  const result = await screen.findByRole("link", { name: /Replay safety/ });
  expect(result.getAttribute("href")).toBe("/docs/engineering/replay-safety");
  expect(fetch).toHaveBeenCalledWith("/chronoverse/docs/search-index.json");
  fireEvent.click(screen.getByRole("button", { name: "Close" }));
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
});
