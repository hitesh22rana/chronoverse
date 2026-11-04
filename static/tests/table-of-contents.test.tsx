import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { TableOfContents } from "@/components/docs/table-of-contents";
import type { DocHeading } from "@/lib/docs";

const headings: DocHeading[] = [{ id: "first", title: "First", level: 2 }, { id: "second", title: "Second", level: 3 }];
let notify: IntersectionObserverCallback;
let update: FrameRequestCallback | undefined;
let disconnect: ReturnType<typeof vi.fn>;
let observe: ReturnType<typeof vi.fn>;
let cancelFrame: ReturnType<typeof vi.fn>;

beforeEach(() => {
  document.body.innerHTML = '<h2 id="first">First section</h2><h3 id="second">Second section</h3>';
  history.replaceState({}, "", "/docs/intro?mode=read");
  Object.defineProperty(document.documentElement, "scrollHeight", { configurable: true, value: 2000 });
  vi.stubGlobal("innerHeight", 800); vi.stubGlobal("scrollY", 0);
  disconnect = vi.fn(); observe = vi.fn(); cancelFrame = vi.fn(); update = undefined;
  vi.stubGlobal("IntersectionObserver", class {
    constructor(callback: IntersectionObserverCallback) { notify = callback; }
    observe = observe; disconnect = disconnect;
  });
  vi.stubGlobal("requestAnimationFrame", vi.fn((callback: FrameRequestCallback) => { update = callback; return 7; }));
  vi.stubGlobal("cancelAnimationFrame", cancelFrame);
  vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockImplementation(function (this: HTMLElement) {
    return { top: this.id === "first" ? 20 : 400 } as DOMRect;
  });
});
afterEach(() => { cleanup(); vi.restoreAllMocks(); vi.unstubAllGlobals(); document.body.innerHTML = ""; });
function flushFrame() { const pending = update; update = undefined; act(() => pending?.(0)); }
function intersect(id: string, isIntersecting: boolean) {
  act(() => notify([{ target: document.getElementById(id)!, isIntersecting, boundingClientRect: {} as DOMRectReadOnly, intersectionRatio: isIntersecting ? 1 : 0, intersectionRect: {} as DOMRectReadOnly, rootBounds: null, time: 0 }], {} as IntersectionObserver));
}

it("renders no navigation when there are no headings and avoids observing missing elements", () => {
  const { rerender } = render(<TableOfContents headings={[]} />);
  expect(screen.queryByLabelText("On this page")).toBeNull();
  rerender(<TableOfContents headings={[{ id: "missing", title: "Missing", level: 2 }]} />);
  expect(observe).not.toHaveBeenCalled();
  expect(requestAnimationFrame).not.toHaveBeenCalled();
});

it("tracks visible headings, preserves query/path, coalesces frames and cleans up", () => {
  const removed = vi.spyOn(window, "removeEventListener");
  const { unmount } = render(<TableOfContents headings={headings} />);
  expect(observe).toHaveBeenCalledTimes(2);
  flushFrame();
  expect(location.href).toContain("/docs/intro?mode=read#first");
  expect(screen.getByRole("link", { name: "First" }).getAttribute("aria-current")).toBe("location");
  intersect("second", true); fireEvent.scroll(window); fireEvent.resize(window);
  expect(requestAnimationFrame).toHaveBeenCalledTimes(2);
  flushFrame();
  expect(screen.getByRole("link", { name: "Second" }).getAttribute("aria-current")).toBe("location");
  expect(screen.getByRole("link", { name: "Second" }).className).toContain("nested");
  expect(location.hash).toBe("#second");
  intersect("second", false); flushFrame();
  expect(location.hash).toBe("#first");
  fireEvent.scroll(window); unmount();
  expect(cancelFrame).toHaveBeenCalledWith(7);
  expect(disconnect).toHaveBeenCalledOnce();
  for (const event of ["scroll", "resize", "hashchange"]) expect(removed.mock.calls.some(([name]) => name === event)).toBe(true);
});

it("uses the last heading at document bottom even when an earlier one is visible", () => {
  render(<TableOfContents headings={headings} />); flushFrame();
  vi.stubGlobal("scrollY", 1200); intersect("first", true); flushFrame();
  expect(location.hash).toBe("#second");
});

it("uses heading order rather than observer arrival order and falls back before first heading", () => {
  render(<TableOfContents headings={headings} />); flushFrame();
  intersect("second", true); intersect("first", true); flushFrame();
  expect(location.hash).toBe("#first");
  intersect("first", false); intersect("second", false);
  vi.mocked(HTMLElement.prototype.getBoundingClientRect).mockReturnValue({ top: 500 } as DOMRect);
  flushFrame();
  expect(location.hash).toBe("#first");
});

it("syncs valid hashes, ignores unknown hashes and avoids redundant history writes", () => {
  history.replaceState({}, "", "/docs/intro?mode=read#second");
  const replace = vi.spyOn(history, "replaceState");
  render(<TableOfContents headings={headings} />);
  expect(screen.getByRole("link", { name: "Second" }).getAttribute("aria-current")).toBe("location");
  history.replaceState({}, "", "/docs/intro?mode=read#missing"); fireEvent(window, new HashChangeEvent("hashchange"));
  expect(screen.getByRole("link", { name: "Second" }).getAttribute("aria-current")).toBe("location");
  flushFrame(); expect(location.hash).toBe("#first");
  replace.mockClear(); fireEvent.scroll(window); flushFrame();
  expect(replace).not.toHaveBeenCalled();
});
