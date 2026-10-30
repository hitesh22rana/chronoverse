import { afterEach, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { renderToStaticMarkup } from "react-dom/server";
import Home from "@/app/page";
import About from "@/app/about/page";
import Privacy from "@/app/privacy/page";
import Terms from "@/app/terms/page";
import Layout from "@/app/layout";
import { Brand } from "@/components/brand";
import { DocShell } from "@/components/docs/doc-shell";
import { Sheet, SheetContent, SheetDescription, SheetFooter, SheetTitle } from "@/components/ui/sheet";
import { CardFooter } from "@/components/ui/card";
import { Dialog, DialogContent, DialogFooter, DialogTitle } from "@/components/ui/dialog";
import { Separator } from "@/components/ui/separator";
import { SearchProvider } from "@/components/docs/search-dialog";

vi.mock("next-themes", () => ({ ThemeProvider: ({ children }: { children: React.ReactNode }) => children, useTheme: () => ({ resolvedTheme: "light", setTheme: vi.fn() }) }));
vi.mock("next/dynamic", () => ({ default: () => () => null }));
afterEach(() => { cleanup(); vi.restoreAllMocks(); vi.unstubAllEnvs(); });

it("renders public pages with actual metadata and working FAQ disclosure", () => {
  const { unmount } = render(<Home />);
  expect(document.querySelectorAll('script[type="application/ld+json"]').length).toBeGreaterThan(0);
  expect(screen.getAllByRole("link").some((link) => link.getAttribute("href")?.includes("/docs"))).toBe(true);
  const toggle = screen.getAllByRole("button").find((button) => button.hasAttribute("aria-expanded"))!;
  expect(toggle.getAttribute("aria-expanded")).toBe("true");
  fireEvent.click(toggle);
  expect(toggle.getAttribute("aria-expanded")).toBe("false");
  unmount();
  for (const [Page, title] of [[About, "What Chronoverse is."], [Privacy, "What this site collects."], [Terms, "Terms of use."]] as const) {
    const view = render(<Page />);
    expect(screen.getByRole("heading", { name: title })).toBeTruthy();
    expect(view.container.querySelector('script[type="application/ld+json"]')).toBeTruthy();
    view.unmount();
  }
});

it("composes header footer skip navigation and production-only CSP", () => {
  vi.stubEnv("NODE_ENV", "production");
  const production = renderToStaticMarkup(<Layout><p>Page body</p></Layout>);
  expect(production).toContain('http-equiv="Content-Security-Policy"');
  expect(production).toContain("default-src &#x27;self&#x27;");
  expect(production).toContain('href="#main-content"');
  expect(production).toContain("Footer navigation");
  vi.stubEnv("NODE_ENV", "development");
  expect(renderToStaticMarkup(<Layout><p>Page body</p></Layout>)).not.toContain("Content-Security-Policy");
});

it("uses lazy brand imagery by default and eager loading when requested", () => {
  const { container, rerender } = render(<Brand />);
  expect(container.querySelector("img")?.getAttribute("loading")).toBe("lazy");
  rerender(<Brand eager />);
  expect(container.querySelector("img")?.getAttribute("loading")).toBe("eager");
  expect(container.querySelector("img")?.getAttribute("srcset")).toContain("32w");
});

it("renders article and index breadcrumbs and adjacent documentation links", () => {
  const shell = (slug: string) => <SearchProvider><DocShell activeSlug={slug} description="Page description" headings={[]} title="A page" previous={{ slug: "previous", title: "Earlier" }} next={{ slug: "next", title: "Later" }}><p>Body</p></DocShell></SearchProvider>;
  const { container, rerender } = render(shell("api/reference/registerUser"));
  expect(screen.getByRole("link", { name: /Earlier/ }).getAttribute("href")).toBe("/docs/previous");
  expect(screen.getByRole("link", { name: /Later/ }).getAttribute("href")).toBe("/docs/next");
  expect(container.querySelector('a[aria-current="page"]')?.getAttribute("href")).toBe("/docs/api/reference/registerUser");
  let graph = JSON.parse(container.querySelector('script[type="application/ld+json"]')!.textContent!)["@graph"];
  expect(graph[0]["@type"]).toBe("TechArticle");
  fireEvent.click(screen.getByRole("button", { name: /Browse docs/ }));
  expect(screen.getByRole("dialog")).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Close" }));
  rerender(shell("unknown"));
  expect(container.querySelector('[aria-label="Breadcrumb"]')?.textContent).toContain("Documentation");
  rerender(<SearchProvider><DocShell activeSlug="introduction" canonicalPath="/docs" description="Index" headings={[]} title="Docs index">Index</DocShell></SearchProvider>);
  graph = JSON.parse(container.querySelector('script[type="application/ld+json"]')!.textContent!)["@graph"];
  expect(graph[0]["@type"]).toBe("CollectionPage");
  expect(graph[1].itemListElement).toHaveLength(2);
});

it("preserves forwarded content and semantic separators in exported primitives", () => {
  const { container } = render(<><CardFooter data-testid="footer">Footer content</CardFooter><Separator decorative={false} orientation="vertical" /><Sheet open><SheetContent><SheetTitle>Panel</SheetTitle><SheetDescription>Panel details</SheetDescription><SheetFooter>Panel action</SheetFooter></SheetContent></Sheet><Dialog open><DialogContent aria-describedby={undefined}><DialogTitle>Dialog</DialogTitle><DialogFooter>Dialog action</DialogFooter></DialogContent></Dialog></>);
  expect(screen.getByTestId("footer").textContent).toBe("Footer content");
  expect(container.querySelector('[role="separator"]')?.getAttribute("aria-orientation")).toBe("vertical");
  expect(screen.getByText("Panel details")).toBeTruthy();
  expect(screen.getByText("Panel action")).toBeTruthy();
  expect(screen.getByText("Dialog action")).toBeTruthy();
});
