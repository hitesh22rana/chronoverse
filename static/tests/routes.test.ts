// @vitest-environment node
import fs from "node:fs";
import { afterEach, expect, it, vi } from "vitest";
import sitemap from "@/app/sitemap";
import robots from "@/app/robots";
import { GET as openapi } from "@/app/docs/openapi.yaml/route";
import { GET as search } from "@/app/docs/search-index.json/route";
import { docPages } from "../docs.config";
import { getOpenApiOperations, getOpenApiPath } from "@/lib/openapi";
import { getSearchDocuments } from "@/lib/docs";
import { sitePageUrl, siteAssetUrl, withBasePath } from "@/lib/site";

afterEach(() => vi.useRealTimers());
it("exposes every documentation/API route with canonical URLs and stable metadata", () => {
  const now = new Date("2026-01-01T00:00:00Z"); vi.useFakeTimers(); vi.setSystemTime(now);
  const entries = sitemap();
  expect(new Set(entries.map((item) => item.url)).size).toBe(entries.length);
  expect(entries.find((item) => item.url === sitePageUrl())).toMatchObject({ priority: 1, changeFrequency: "weekly", lastModified: now });
  expect(entries.find((item) => item.url === sitePageUrl("/docs"))?.priority).toBe(0.9);
  for (const page of docPages) expect(entries.some((entry) => entry.url === sitePageUrl(`/docs/${page.slug}`))).toBe(true);
  for (const operation of getOpenApiOperations()) expect(entries.find((entry) => entry.url === sitePageUrl(`/docs/api/reference/${operation.operationId}`))).toMatchObject({ changeFrequency: "monthly", priority: 0.7 });
  expect(robots()).toEqual({ rules: { userAgent: "*", allow: withBasePath("/") }, sitemap: siteAssetUrl("/sitemap.xml") });
});
it("serves the original OpenAPI document and full search index with correct MIME", async () => {
  const response = openapi();
  expect(response.status).toBe(200);
  expect(response.headers.get("content-type")).toBe("application/yaml; charset=utf-8");
  expect(await response.text()).toBe(fs.readFileSync(getOpenApiPath(), "utf8"));
  const index = search();
  expect(index.status).toBe(200);
  expect(index.headers.get("content-type")).toContain("application/json");
  expect(await index.json()).toEqual(getSearchDocuments());
});
