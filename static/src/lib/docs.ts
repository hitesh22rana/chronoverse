import fs from "node:fs";
import path from "node:path";

import { docPages, docsConfig, getDocPage } from "../../docs.config";
import { getOpenApiOperations } from "@/lib/openapi";

export type DocHeading = { id: string; title: string; level: 2 | 3 };

export function getDocSourcePath(source: string) {
  return path.join(process.cwd(), "content/docs", `${source}.mdx`);
}

export function getDocSource(source: string) {
  return fs.readFileSync(getDocSourcePath(source), "utf8");
}

function slugify(value: string) {
  return value
    .toLowerCase()
    .replace(/[`*_]/g, "")
    .replace(/[^a-z0-9\s-]/g, "")
    .trim()
    .replace(/\s+/g, "-");
}

export function getDocHeadings(source: string): DocHeading[] {
  return getDocSource(source)
    .split("\n")
    .map((line) => {
      const match = /^(##|###)\s+(.+)$/.exec(line.trim());
      if (!match) return null;
      const title = match[2].replace(/<[^>]+>/g, "").replace(/[`*_]/g, "");
      return { id: slugify(title), title, level: match[1].length as 2 | 3 };
    })
    .filter((heading): heading is DocHeading => heading !== null);
}

export function getDocNavigation(slug: string) {
  const operations = getOpenApiOperations().map((operation) => ({
    slug: `api/reference/${operation.operationId}`,
    title: operation.summary,
    description: `${operation.method} ${operation.path}`,
    source: "",
    sourceRefs: ["internal/server/server.go"],
  }));
  const referenceIndex = docPages.findIndex((page) => page.slug === "api/reference");
  const pages = [...docPages];
  pages.splice(referenceIndex + 1, 0, ...operations);
  const index = pages.findIndex((page) => page.slug === slug);
  return { previous: index > 0 ? pages[index - 1] : undefined, next: index >= 0 ? pages[index + 1] : undefined };
}

export function getSearchDocuments() {
  const docs = docPages.map((page) => {
    const source = getDocSource(page.source);
    const section = docsConfig.find((group) => group.pages.some((candidate) => candidate.slug === page.slug))?.title ?? "Documentation";
    const headings = getDocHeadings(page.source).map((heading) => heading.title).join(" ");
    const text = source
      .replace(/```[\s\S]*?```/g, " ")
      .replace(/!\[([^\]]*)\]\([^)]*\)/g, "$1")
      .replace(/\[([^\]]+)\]\([^)]*\)/g, "$1")
      .replace(/<[^>]+>/g, " ")
      .replace(/[#*`_{}[\]()]/g, " ")
      .replace(/\s+/g, " ")
      .trim();
    return { title: page.title, description: page.description, headings, href: `/docs/${page.slug}`, section, text };
  });
  const operations = getOpenApiOperations().map((operation) => ({
    title: operation.summary,
    description: `${operation.method} ${operation.path}`,
    href: `/docs/api/reference/${operation.operationId}`,
    section: "API Reference",
    headings: `${operation.method} ${operation.path} ${operation.tags.join(" ")}`,
    text: `${operation.description ?? ""} ${operation.tags.join(" ")}`,
  }));
  return [...docs, ...operations];
}

export function isGeneratedApiSlug(slug: string) {
  return slug.startsWith("api/reference/");
}

export function getPageMetadata(slug: string) {
  if (isGeneratedApiSlug(slug)) {
    const operationId = slug.split("/").at(-1) ?? "";
    const operation = getOpenApiOperations().find((item) => item.operationId === operationId);
    return operation ? { title: operation.summary, description: `${operation.method} ${operation.path}` } : undefined;
  }
  return getDocPage(slug);
}
