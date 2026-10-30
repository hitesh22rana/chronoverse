// @vitest-environment node
import { expect, it, vi } from "vitest";
import type { ReactElement } from "react";
import DocumentationPage, { generateMetadata, generateStaticParams, dynamicParams } from "@/app/docs/[...slug]/page";
import DocsIndex from "@/app/docs/page";
import { docPages } from "../docs.config";
import { getOpenApiOperations } from "@/lib/openapi";
import { sitePageUrl } from "@/lib/site";

vi.setConfig({ testTimeout: 30000 });

vi.mock("next/navigation", () => ({ notFound: () => { throw new Error("not-found"); } }));
const params = (slug: string) => ({ params: Promise.resolve({ slug: slug.split("/") }) });

it("generates every configured source and API route exactly once", () => {
  expect(dynamicParams).toBe(false);
  const routes = generateStaticParams().map(({ slug }) => slug.join("/"));
  expect(new Set(routes).size).toBe(routes.length);
  expect(routes).toEqual([...docPages.map((page) => page.slug), ...getOpenApiOperations().map((operation) => `api/reference/${operation.operationId}`)]);
});

it("loads every actual MDX module and returns matching navigation and metadata", async () => {
  for (const page of docPages) {
    const result = await DocumentationPage(params(page.slug));
    expect(result.props.activeSlug).toBe(page.slug);
    expect(result.props.title).toBe(page.title);
    expect(typeof (result.props.children as ReactElement).type).toBe("function");
    expect(await generateMetadata(params(page.slug))).toMatchObject({
      title: `${page.title} | Docs`, description: page.description, alternates: { canonical: sitePageUrl(`/docs/${page.slug}`) },
    });
  }
  const index = DocsIndex();
  expect(index.props.canonicalPath).toBe("/docs");
  expect(index.props.next).toBeTruthy();
});

it("renders every generated API route with real operation-specific headings", async () => {
  for (const operation of getOpenApiOperations()) {
    const result = await DocumentationPage(params(`api/reference/${operation.operationId}`));
    expect(result.props.title).toBe(operation.summary);
    expect(result.props.headings.some(({ id }: { id: string }) => id === "request-body")).toBe(Boolean(operation.requestBody));
    expect((result.props.children as ReactElement<{ operation: unknown }>).props.operation).toEqual(operation);
  }
});

it("rejects unknown source and API routes rather than rendering empty documentation", async () => {
  expect(await generateMetadata(params("missing"))).toEqual({});
  for (const slug of ["missing", "api/reference/missing"]) await expect(DocumentationPage(params(slug))).rejects.toThrow("not-found");
});
