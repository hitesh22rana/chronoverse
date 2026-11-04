// @vitest-environment node
import fs from "node:fs";
import { afterEach, describe, expect, it, vi } from "vitest";
import { docPages } from "../docs.config";
import { getDocSource, getDocSourcePath, getDocHeadings, getDocNavigation, getSearchDocuments, getPageMetadata, isGeneratedApiSlug, getApiOperationDescription } from "@/lib/docs";
import { getOpenApiOperations } from "@/lib/openapi";
import { BASE_PATH, SITE_URL, withBasePath, sitePageUrl, siteAssetUrl } from "@/lib/site";
import { cn } from "@/lib/utils";

afterEach(() => vi.restoreAllMocks());

describe("documentation data contracts", () => {
  it("reads each registered source and extracts only level-two/three headings", () => {
    for (const page of docPages) {
      expect(getDocSourcePath(page.source)).toBe(`${process.cwd()}/content/docs/${page.source}.mdx`);
      expect(getDocSource(page.source).trim().length).toBeGreaterThan(0);
      expect(getPageMetadata(page.slug)?.title).toBe(page.title);
    }
    vi.spyOn(fs, "readFileSync").mockReturnValue("# Root\n## **Hello** <em>world!</em>\n### `A_b` *c*\n#### Too deep\nplain");
    expect(getDocHeadings("fixture")).toEqual([
      { id: "hello-world", title: "Hello world!", level: 2 },
      { id: "ab-c", title: "Ab c", level: 3 },
    ]);
  });

  it("inserts generated operations after the API index with correct navigation boundaries", () => {
    const operations = getOpenApiOperations();
    expect(getDocNavigation(docPages[0].slug).previous).toBeUndefined();
    expect(getDocNavigation(docPages.at(-1)!.slug).next).toBeUndefined();
    expect(getDocNavigation("missing")).toEqual({ previous: undefined, next: undefined });
    expect(getDocNavigation("api/reference").next?.slug).toBe(`api/reference/${operations[0].operationId}`);
    expect(getDocNavigation(`api/reference/${operations[0].operationId}`).previous?.slug).toBe("api/reference");
    for (const operation of operations) {
      const slug = `api/reference/${operation.operationId}`;
      expect(isGeneratedApiSlug(slug)).toBe(true);
      expect(getPageMetadata(slug)).toEqual({ title: operation.summary, description: getApiOperationDescription(operation) });
    }
    expect(isGeneratedApiSlug("api/reference")).toBe(false);
    expect(getPageMetadata("missing")).toBeUndefined();
    expect(getPageMetadata("api/reference/missing")).toBeUndefined();
  });

  it("builds a complete searchable index with source text rather than markdown syntax", () => {
    const operations = getOpenApiOperations();
    const index = getSearchDocuments();
    expect(index).toHaveLength(docPages.length + operations.length);
    for (const page of docPages) {
      const entry = index.find((item) => item.href === `/docs/${page.slug}`)!;
      expect(entry.title).toBe(page.title);
      expect(entry.section).not.toBe("");
      expect(entry.text).not.toContain("```");
      expect(entry.text).not.toMatch(/<\/?[A-Za-z][^>]*>/);
    }
    for (const operation of operations) {
      const entry = index.find((item) => item.href === `/docs/api/reference/${operation.operationId}`)!;
      expect(entry.description).toBe(`${operation.method} ${operation.path}`);
      expect(entry.section).toBe("API Reference");
    }
  });
});

it("normalizes canonical page URLs independently from asset URLs and external links", () => {
  expect(withBasePath("/docs")).toBe(`${BASE_PATH}/docs`);
  expect(withBasePath("https://example.com/docs")).toBe("https://example.com/docs");
  expect(sitePageUrl()).toBe(`${SITE_URL}/`);
  expect(sitePageUrl("docs")).toBe(`${SITE_URL}/docs/`);
  expect(sitePageUrl("/docs/")).toBe(`${SITE_URL}/docs/`);
  expect(siteAssetUrl("image.png")).toBe(`${SITE_URL}/image.png`);
  expect(siteAssetUrl("/image.png")).toBe(`${SITE_URL}/image.png`);
  expect(cn("px-2", false, { "font-bold": true }, "px-4")).toBe("font-bold px-4");
});
