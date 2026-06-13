import fs from "node:fs";
import path from "node:path";

import SwaggerParser from "@apidevtools/swagger-parser";

import { docPages } from "../docs.config";
import { getDocSource, getDocSourcePath } from "../src/lib/docs";
import { getOpenApiOperations, getOpenApiPath } from "../src/lib/openapi";

const errors: string[] = [];
const slugs = new Set<string>();

function containsReference(value: unknown): boolean {
  if (Array.isArray(value)) return value.some(containsReference);
  if (typeof value !== "object" || value === null) return false;
  const record = value as Record<string, unknown>;
  return typeof record.$ref === "string" || Object.values(record).some(containsReference);
}

for (const page of docPages) {
  if (!page.slug || !page.title || !page.description || !page.source) {
    errors.push(`Malformed documentation metadata for ${page.slug || "<unknown>"}`);
  }
  if (slugs.has(page.slug)) errors.push(`Duplicate documentation slug: ${page.slug}`);
  slugs.add(page.slug);

  if (!fs.existsSync(getDocSourcePath(page.source))) errors.push(`Missing MDX source for ${page.slug}: ${page.source}.mdx`);
  for (const sourceRef of page.sourceRefs) {
    if (!fs.existsSync(path.join(process.cwd(), "..", sourceRef))) errors.push(`Missing repository source reference on ${page.slug}: ${sourceRef}`);
  }
}

const contentRoot = path.join(process.cwd(), "content/docs");
function collectMdxFiles(directory: string): string[] {
  return fs.readdirSync(directory, { withFileTypes: true }).flatMap((entry) => {
    const fullPath = path.join(directory, entry.name);
    return entry.isDirectory() ? collectMdxFiles(fullPath) : entry.name.endsWith(".mdx") ? [fullPath] : [];
  });
}

const configuredSources = new Set(docPages.map((page) => path.resolve(getDocSourcePath(page.source))));
for (const file of collectMdxFiles(contentRoot)) {
  if (!configuredSources.has(path.resolve(file))) errors.push(`Unregistered MDX file: ${path.relative(contentRoot, file)}`);
}

await SwaggerParser.validate(getOpenApiPath()).catch((error: unknown) => errors.push(`Invalid OpenAPI document: ${String(error)}`));
const operations = getOpenApiOperations();
for (const operation of operations) {
  if (containsReference(operation)) errors.push(`Unresolved OpenAPI reference in generated operation ${operation.operationId}`);
  for (const [status, response] of Object.entries(operation.responses)) {
    if (!response.description) errors.push(`Missing response description for ${operation.operationId} HTTP ${status}`);
  }
}
const operationSlugs = new Set(operations.map((operation) => `api/reference/${operation.operationId}`));
const validSlugs = new Set([...slugs, ...operationSlugs]);

for (const page of docPages) {
  if (!fs.existsSync(getDocSourcePath(page.source))) continue;
  const source = getDocSource(page.source);
  for (const match of source.matchAll(/\]\((\/docs\/[^)#?]+)(?:#[^)]+)?\)/g)) {
    const target = match[1].replace(/^\/docs\/?/, "").replace(/\/$/, "");
    if (target && !validSlugs.has(target)) errors.push(`Broken internal docs link in ${page.slug}: ${match[1]}`);
  }
}

if (errors.length > 0) {
  console.error(errors.map((error) => `- ${error}`).join("\n"));
  process.exit(1);
}

console.log(`Validated ${docPages.length} MDX pages and ${operationSlugs.size} OpenAPI operations.`);
