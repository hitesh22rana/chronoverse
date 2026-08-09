import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

import { parse } from "yaml";

import { docsConfig, type DocPage } from "../docs.config";

type Section = {
  title: string;
  pages: DocPage[];
};

const scriptDirectory = path.dirname(fileURLToPath(import.meta.url));
const staticRoot = path.resolve(scriptDirectory, "..");
const repositoryRoot = path.resolve(staticRoot, "..");
const checkOnly = process.argv.includes("--check");

const sectionDefinitions = [
  { title: "Onboarding", groups: ["Getting Started"] },
  { title: "Architecture", groups: ["Core Concepts", "Engineering"] },
  { title: "Getting Started", groups: ["Features", "HTTP API", "Deployment"] },
  { title: "Deep Dive", groups: ["Internal Contracts", "Operations"] },
  { title: "Optional", groups: ["Contributing"] },
];

const groupsByTitle = new Map(docsConfig.map((group) => [group.title, group]));
const sections: Section[] = sectionDefinitions.map((definition) => ({
  title: definition.title,
  pages: definition.groups.flatMap((title) => {
    const group = groupsByTitle.get(title);
    if (!group) throw new Error(`Unknown documentation group: ${title}`);
    return group.pages;
  }),
}));

const configuredPages = docsConfig.flatMap((group) => group.pages);
const organizedPages = sections.flatMap((section) => section.pages);
const organizedSlugs = new Set(organizedPages.map((page) => page.slug));
if (organizedPages.length !== organizedSlugs.size) {
  throw new Error("A documentation page appears in more than one LLM section");
}
const missingPages = configuredPages.filter((page) => !organizedSlugs.has(page.slug));
if (missingPages.length > 0) {
  throw new Error(`Documentation pages are missing from LLM sections: ${missingPages.map((page) => page.slug).join(", ")}`);
}

const compactSlugs = new Set([
  "introduction",
  "quickstart",
  "installation",
  "getting-started/first-workflow",
  "concepts/workflows",
  "concepts/jobs",
  "engineering/architecture",
  "engineering/replay-safety",
  "engineering/transactional-outbox",
  "engineering/job-leases",
  "engineering/kafka-processing",
  "engineering/logging-search-pipeline",
  "features/log-streaming",
  "api/overview",
  "api/request-safety",
  "api/reference",
  "deployment/kubernetes",
  "deployment/configuration",
  "deployment/security",
  "operations/observability",
  "operations/recovery",
  "contributing/repository-layout",
  "contributing/development",
]);

const unknownCompactSlugs = [...compactSlugs].filter((slug) => !organizedSlugs.has(slug));
if (unknownCompactSlugs.length > 0) {
  throw new Error(`Compact LLM index contains unknown pages: ${unknownCompactSlugs.join(", ")}`);
}

const descriptionOverrides = new Map([
  ["introduction", "Supported HEARTBEAT and CONTAINER workloads, platform capabilities, runtime shape, and failure-recovery guarantees."],
  ["quickstart", "Docker Compose prerequisites, startup commands, readiness checks, local endpoints, and shutdown."],
  ["installation", "Docker runtime requirements, Go and Node developer toolchains, generated dependencies, and production preparation."],
  ["getting-started/first-workflow", "Session authentication, container workflow creation, build polling, manual execution, and log inspection."],
  ["engineering/architecture", "HTTP and gRPC domain boundaries, Kafka workers, persistence systems, runtime-node ownership, and Docker execution locality."],
  ["engineering/replay-safety", "Idempotency, outbox publication, stale-event rejection, deterministic side effects, durable leases, and ordered Kafka commits."],
  ["engineering/logging-search-pipeline", "Live Redis/SSE output and replay-safe retained-log ingestion through Kafka, ClickHouse, and Meilisearch."],
  ["api/overview", "Public API base paths, cookie sessions, JSON behavior, idempotent mutations, and the OpenAPI reference."],
  ["api/request-safety", "CSRF checks, Idempotency-Key rules, atomic mutation records, and conflict behavior for retrying clients."],
  ["deployment/kubernetes", "Local and production Kustomize overlays, prerequisites, secrets, storage, scaling, runtime agents, and validation commands."],
  ["deployment/security", "Certificate bootstrap, infrastructure TLS, gRPC authorization, browser protections, Docker access, and production secrets."],
  ["operations/observability", "OpenTelemetry traces, metrics, and structured logs, including PromQL examples and an investigation sequence."],
]);

const summary = "> Chronoverse is a self-hosted distributed scheduler and orchestrator for teams running interval-triggered or manual HEARTBEAT and Docker CONTAINER workflows, built with Go HTTP/gRPC services, Kafka workers, PostgreSQL, Redis, ClickHouse, Meilisearch, Docker, React/Next.js, and OpenTelemetry.";

const context = [
  "Chronoverse expects at-least-once delivery and partial failures. It relies on idempotency keys, transactional outbox publication, workflow generations, deterministic event IDs, stale-event guards, durable leases, and partition-aware Kafka commits rather than globally exactly-once execution.",
  "The HTTP API uses cookie sessions and CSRF validation; retry-prone workflow mutations require an `Idempotency-Key`. HEARTBEAT workflows have no logs. Retention-disabled reads return `412 Precondition Failed`, while SSE uses error frames after streaming starts.",
  "Container ownership pairs the container ID with its assigned runtime node and persisted Docker endpoint; workers preserve this locality for execution, logs, cleanup, and recovery.",
  "Canonical documentation sources are MDX files in `static/content/docs` and the OpenAPI document in `static/content/openapi.yaml`. They are published at https://hitesh22rana.github.io/chronoverse/ from https://github.com/hitesh22rana/chronoverse.",
].join("\n\n");

function stripFrontmatter(markdown: string) {
  if (!markdown.startsWith("---\n")) return markdown.trim();
  const end = markdown.indexOf("\n---\n", 4);
  if (end === -1) throw new Error("Unclosed YAML frontmatter block");
  return markdown.slice(end + 5).trim();
}

function rewritePublishedLinks(markdown: string) {
  return markdown
    .replace(/\]\(\/docs(?=\/|\))/g, "](./docs")
    .replace(/href=(["'])\/docs(?=\/|["'])/g, "href=$1./docs");
}

function renderCompact(linkForPage: (page: DocPage) => string) {
  const output = ["# Chronoverse", "", summary, "", context];
  for (const section of sections) {
    const selected = section.pages.filter((page) => compactSlugs.has(page.slug));
    if (selected.length === 0) continue;
    output.push("", `## ${section.title}`, "");
    for (const page of selected) {
      const description = descriptionOverrides.get(page.slug) ?? page.description;
      output.push(`- [${page.title}](${linkForPage(page)}): ${description}`);
    }
  }
  return `${output.join("\n")}\n`;
}

type OpenApiDocument = {
  paths?: Record<string, Record<string, { operationId?: unknown }>>;
};

function readOpenApi() {
  const source = fs.readFileSync(path.join(staticRoot, "content/openapi.yaml"), "utf8").trim();
  const document = parse(source) as OpenApiDocument;
  const methods = new Set(["get", "post", "put", "patch", "delete"]);
  const operationIds = Object.values(document.paths ?? {}).flatMap((pathItem) =>
    Object.entries(pathItem)
      .filter(([method]) => methods.has(method))
      .map(([, operation]) => String(operation.operationId)),
  );
  if (operationIds.some((operationId) => !operationId || operationId === "undefined")) {
    throw new Error("Every OpenAPI operation must define operationId");
  }
  if (operationIds.length !== new Set(operationIds).size) {
    throw new Error("OpenAPI operationId values must be unique");
  }
  return { source, operationCount: operationIds.length };
}

function renderFull() {
  const { source: openApi, operationCount } = readOpenApi();
  const output = ["# Chronoverse", "", summary, "", context];
  for (const section of sections) {
    output.push("", `## ${section.title}`);
    for (const page of section.pages) {
      const sourcePath = path.join(staticRoot, "content/docs", `${page.source}.mdx`);
      const content = rewritePublishedLinks(stripFrontmatter(fs.readFileSync(sourcePath, "utf8")));
      if (!content) throw new Error(`Empty documentation page: ${page.slug}`);
      output.push("", `<doc title="${page.title}" path="./docs/${page.slug}/">`, content, "</doc>");
    }
    if (section.title === "Getting Started") {
      output.push(
        "",
        '<doc title="OpenAPI specification" path="./docs/openapi.yaml">',
        "# OpenAPI specification",
        "",
        `This canonical OpenAPI 3.1 document defines all ${operationCount} generated HTTP operations, including authentication requirements, parameters, request bodies, responses, and shared schemas.`,
        "",
        "```yaml",
        openApi,
        "```",
        "</doc>",
      );
    }
  }
  return `${output.join("\n")}\n`;
}

const generatedFiles = new Map([
  [path.join(repositoryRoot, "llms.txt"), renderCompact((page) => `./static/content/docs/${page.source}.mdx`)],
  [path.join(staticRoot, "public/llms.txt"), renderCompact((page) => `./docs/${page.slug}/`)],
  [path.join(staticRoot, "public/llms-full.txt"), renderFull()],
]);

for (const file of [path.join(repositoryRoot, "llms.txt"), path.join(staticRoot, "public/llms.txt")]) {
  const size = Buffer.byteLength(generatedFiles.get(file) ?? "");
  if (size < 1000 || size > 5000) {
    throw new Error(`${path.relative(repositoryRoot, file)} must be between 1 KB and 5 KB; generated ${size} bytes`);
  }
}

const generatedFull = generatedFiles.get(path.join(staticRoot, "public/llms-full.txt")) ?? "";
if (/\]\(\/docs(?:\/|\))|href=(["'])\/docs(?:\/|\1)/.test(generatedFull)) {
  throw new Error("Generated llms-full.txt contains a root-relative documentation link");
}
const docBlocks = [...generatedFull.matchAll(/<doc title="[^"]+" path="[^"]+">\n([\s\S]*?)\n<\/doc>/g)];
if (docBlocks.length !== configuredPages.length + 1 || docBlocks.some((match) => !match[1].trim())) {
  throw new Error("Generated llms-full.txt must contain every documentation page and the OpenAPI specification");
}

if (checkOnly) {
  const staleFiles = [...generatedFiles].flatMap(([file, expected]) => {
    if (!fs.existsSync(file)) return [path.relative(repositoryRoot, file)];
    return fs.readFileSync(file, "utf8") === expected ? [] : [path.relative(repositoryRoot, file)];
  });
  if (staleFiles.length > 0) {
    console.error(`LLM documentation is stale: ${staleFiles.join(", ")}`);
    console.error("Run `npm run generate:llms` from static/ and commit the regenerated files.");
    process.exit(1);
  }
  console.log(`Validated ${generatedFiles.size} generated LLM documentation files.`);
} else {
  for (const [file, content] of generatedFiles) fs.writeFileSync(file, content);
  console.log(`Generated ${generatedFiles.size} LLM documentation files.`);
}
