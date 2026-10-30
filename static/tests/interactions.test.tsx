import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { SWRConfig } from "swr";
import Link from "next/link";
import { SearchPanel } from "@/components/docs/search-panel";
import { SearchProvider, SearchTrigger } from "@/components/docs/search-dialog";
import { ThemeToggle } from "@/components/theme-toggle";
import { CodeBlock } from "@/components/docs/code-block";
import { DocsSidebarScrollArea } from "@/components/docs/docs-sidebar-scroll-area";

Object.defineProperty(HTMLElement.prototype, "scrollTo", { configurable: true, writable: true, value: () => {} });

const theme = vi.hoisted(() => ({ resolvedTheme: "dark", setTheme: vi.fn() }));
vi.mock("next-themes", () => ({ useTheme: () => theme }));
vi.mock("next/dynamic", () => ({ default: () => (props: { onOpenChange: (open: boolean) => void }) => <button onClick={() => props.onOpenChange(false)}>Close search</button> }));

afterEach(() => { cleanup(); vi.restoreAllMocks(); vi.unstubAllGlobals(); vi.useRealTimers(); sessionStorage.clear(); });
beforeEach(() => { theme.setTheme.mockClear(); });

it.each(["dark", "light", undefined])("switches theme based on resolved theme %s", (resolved) => {
  theme.resolvedTheme = resolved as string;
  render(<ThemeToggle />); fireEvent.click(screen.getByRole("button", { name: "Toggle color theme" }));
  expect(theme.setTheme).toHaveBeenCalledWith(resolved === "dark" ? "light" : "dark");
});

it("copies actual displayed code and resets copied state", async () => {
  const writeText = vi.fn().mockResolvedValue(undefined);
  vi.stubGlobal("navigator", { clipboard: { writeText } });
  const { container } = render(<CodeBlock data-language="bash"><code>echo hello</code></CodeBlock>);
  const pre = container.querySelector("pre")!;
  Object.defineProperty(pre, "innerText", { value: "echo hello" });
  fireEvent.click(screen.getByRole("button", { name: "Copy code" }));
  await waitFor(() => expect(writeText).toHaveBeenCalledWith("echo hello"));
  expect(container.querySelector(".lucide-check")).toBeTruthy();
  await waitFor(() => expect(container.querySelector(".lucide-copy")).toBeTruthy(), { timeout: 2200 });
  expect(pre.dataset.language).toBe("bash");
});

describe("search dialog lifecycle", () => {
  it("opens from trigger or keyboard, closes, ignores ordinary keys and removes listeners", () => {
    const removed = vi.spyOn(window, "removeEventListener");
    const { unmount } = render(<SearchProvider><SearchTrigger /><SearchTrigger compact /></SearchProvider>);
    expect(screen.queryByText("Close search")).toBeNull();
    fireEvent.keyDown(window, { key: "k" });
    expect(screen.queryByText("Close search")).toBeNull();
    fireEvent.click(screen.getAllByRole("button", { name: "Search documentation" })[0]);
    fireEvent.click(screen.getByText("Close search"));
    for (const modifiers of [{ ctrlKey: true }, { metaKey: true }]) {
      const event = new KeyboardEvent("keydown", { key: "K", ...modifiers, cancelable: true });
      act(() => window.dispatchEvent(event));
      expect(event.defaultPrevented).toBe(true);
      fireEvent.click(screen.getByText("Close search"));
    }
    unmount();
    expect(removed.mock.calls.some(([name]) => name === "keydown")).toBe(true);
  });

  it("rejects triggers without their provider", () => {
    vi.spyOn(console, "error").mockImplementation(() => {});
    expect(() => render(<SearchTrigger />)).toThrow("SearchTrigger must be rendered inside SearchProvider");
  });
});

type Entry = { title: string; description: string; headings: string; href: string; section: string; text: string };
const entry = (title: string, extra: Partial<Entry> = {}): Entry => ({ title, description: "Guide", headings: "", href: `/docs/${title.replaceAll(" ", "-")}`, section: "Docs", text: "", ...extra });
function searchPanel(documents: Entry[], onOpenChange = vi.fn()) {
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue({ ok: true, json: async () => documents }));
  render(<SWRConfig value={{ provider: () => new Map() }}><SearchPanel onOpenChange={onOpenChange} /></SWRConfig>);
  return onOpenChange;
}

it("shows twelve initial entries, ranks real Fuse matches, trims search and closes on selection", async () => {
  const entries = [entry("Jobs"), entry("Job creation"), entry("Replay job"), entry("Worker", { headings: "Job queue" }), entry("Agents", { text: "job" }), ...Array.from({ length: 15 }, (_, n) => entry(`Guide ${n}`))];
  const close = searchPanel(entries);
  await screen.findByText("Jobs");
  expect(screen.getAllByRole("link")).toHaveLength(12);
  expect(fetch).toHaveBeenCalledWith("/chronoverse/docs/search-index.json");
  fireEvent.change(screen.getByRole("textbox", { name: "Search query" }), { target: { value: "  job  " } });
  await waitFor(() => expect(screen.queryByText("Guide 0")).toBeNull());
  const titles = screen.getAllByRole("link").map((node) => node.querySelector("strong")?.textContent);
  expect(titles.indexOf("Job creation")).toBeLessThan(titles.indexOf("Replay job"));
  expect(titles.indexOf("Replay job")).toBeLessThan(titles.indexOf("Worker"));
  fireEvent.click(screen.getAllByRole("link")[0]);
  expect(close).toHaveBeenCalledWith(false);
  fireEvent.change(screen.getByRole("textbox"), { target: { value: "zzzz-no-match" } });
  await screen.findByText(/No documentation matched/);
  expect(screen.queryAllByRole("link")).toHaveLength(0);
});

it("caps matching results at sixteen and orders exact title before prefix", async () => {
  searchPanel([entry("Job queue"), ...Array.from({ length: 20 }, (_, n) => entry(`Job ${n}`)), entry("Job")]);
  await screen.findByText("Job queue");
  fireEvent.change(screen.getByRole("textbox"), { target: { value: "Job" } });
  await waitFor(() => expect(screen.getAllByRole("link")).toHaveLength(16));
  expect(screen.getAllByRole("link")[0].querySelector("strong")?.textContent).toBe("Job");
});

it("handles a rejected index response without retries or phantom results", async () => {
  const fetcher = vi.fn().mockResolvedValue({ ok: false, status: 503 }); vi.stubGlobal("fetch", fetcher);
  render(<SWRConfig value={{ provider: () => new Map() }}><SearchPanel onOpenChange={vi.fn()} /></SWRConfig>);
  await waitFor(() => expect(fetcher).toHaveBeenCalledTimes(1));
  fireEvent.change(screen.getByRole("textbox"), { target: { value: "job" } });
  await screen.findByText(/No documentation matched/);
  expect(screen.queryAllByRole("link")).toHaveLength(0);
});

it.each(["missing", "NaN", "120"])("restores only finite sidebar positions (%s) and remembers user scroll", (value) => {
  if (value !== "missing") sessionStorage.setItem("chronoverse:docs-sidebar-scroll", value);
  const { container } = render(<DocsSidebarScrollArea activeSlug="x"><Link href="/docs/x">Page</Link></DocsSidebarScrollArea>);
  const aside = container.querySelector("aside")!;
  expect(aside.scrollTop).toBe(value === "120" ? 120 : 0);
  aside.scrollTop = 42; fireEvent.scroll(aside);
  expect(sessionStorage.getItem("chronoverse:docs-sidebar-scroll")).toBe("42");
});

it.each([true, false])("centers offscreen active link with reduced motion %s", (reduced) => {
  sessionStorage.setItem("chronoverse:docs-sidebar-scroll", "10");
  const scrollTo = vi.fn();
  vi.spyOn(HTMLElement.prototype, "scrollTo").mockImplementation(scrollTo);
  vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockImplementation(function (this: HTMLElement) {
    return { top: this.tagName === "ASIDE" ? 20 : 250, bottom: this.tagName === "ASIDE" ? 220 : 270, height: this.tagName === "ASIDE" ? 200 : 20 } as DOMRect;
  });
  vi.spyOn(HTMLElement.prototype, "clientHeight", "get").mockReturnValue(200);
  vi.stubGlobal("matchMedia", vi.fn().mockReturnValue({ matches: reduced }));
  render(<DocsSidebarScrollArea activeSlug="x"><Link href="/docs/x" aria-current="page">Page</Link></DocsSidebarScrollArea>);
  expect(scrollTo).toHaveBeenCalledWith({ top: 150, behavior: reduced ? "auto" : "smooth" });
});

it("does not scroll an already visible current page", () => {
  const scrollTo = vi.fn(); vi.spyOn(HTMLElement.prototype, "scrollTo").mockImplementation(scrollTo);
  render(<DocsSidebarScrollArea activeSlug="x"><a aria-current="page">Page</a></DocsSidebarScrollArea>);
  expect(scrollTo).not.toHaveBeenCalled();
});
