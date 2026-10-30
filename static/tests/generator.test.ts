// @vitest-environment node
import fs from "node:fs";
import path from "node:path";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { docsConfig } from "../docs.config";

const root = process.cwd();
const originalArguments = [...process.argv];
let read: typeof fs.readFileSync;
let exists: typeof fs.existsSync;
let write: ReturnType<typeof vi.fn>;

beforeEach(() => {
  vi.resetModules();
  process.argv = [...originalArguments, "--check"];
  read = fs.readFileSync.bind(fs);
  exists = fs.existsSync.bind(fs);
  write = vi.fn();
  vi.spyOn(console, "log").mockImplementation(() => {});
  vi.spyOn(console, "error").mockImplementation(() => {});
  vi.spyOn(process, "exit").mockImplementation((code) => { throw new Error(`exit:${code}`); });
  vi.doMock("node:fs", () => ({ default: { ...fs, readFileSync: (...args: Parameters<typeof fs.readFileSync>) => read(...args), existsSync: (name: fs.PathLike) => exists(name), writeFileSync: write } }));
});
afterEach(() => {
  process.argv = originalArguments;
  vi.doUnmock("node:fs");
  vi.doUnmock("../docs.config");
  vi.restoreAllMocks();
});
const generate = () => import("../scripts/generate-llms");
function replaceFile(relative: string, transform: (text: string) => string) {
  const previous = read;
  read = ((name: fs.PathOrFileDescriptor, options?: unknown) => {
    const value = previous(name, options as never);
    return path.resolve(String(name)) === path.resolve(root, relative) ? transform(String(value)) : value;
  }) as typeof fs.readFileSync;
}

it("validates committed output without changing files", async () => {
  await generate();
  expect(console.log).toHaveBeenCalledWith("Validated 3 generated LLM documentation files.");
  expect(write).not.toHaveBeenCalled();
});

it("generates every publication with rewritten links and expanded architecture", async () => {
  process.argv = originalArguments;
  await generate();
  expect(write).toHaveBeenCalledTimes(3);
  const full = write.mock.calls.find(([name]) => String(name).endsWith("llms-full.txt"))![1] as string;
  expect(full).toContain("**Architecture flow:**");
  expect(full).toContain("OpenAPI specification");
  expect(full).not.toContain("<ArchitectureMap />");
  expect(full).not.toMatch(/\]\(\/docs/);
  expect(full.match(/<doc title=/g)).toHaveLength(docsConfig.flatMap((group) => group.pages).length + 1);
});

it.each(["missing", "changed"])("rejects %s generated output without writing", async (kind) => {
  if (kind === "missing") {
    const previous = exists;
    exists = (name) => String(name).endsWith("public/llms.txt") ? false : previous(name);
  } else replaceFile("public/llms.txt", () => "old generated content");
  await expect(generate()).rejects.toThrow("exit:1");
  expect(console.error).toHaveBeenCalledWith(expect.stringContaining("LLM documentation is stale:"));
  expect(write).not.toHaveBeenCalled();
});

it.each([
  ["unclosed frontmatter", "---\nmissing closing delimiter", "Unclosed YAML frontmatter"],
  ["empty documentation", "   ", "Empty documentation page"],
  ["unsupported component", "# Contract\n<UnknownWidget />", "Unsupported self-closing MDX components"],
])("rejects %s before publishing", async (_name, content, error) => {
  replaceFile("content/docs/introduction.mdx", () => content);
  await expect(generate()).rejects.toThrow(error);
  expect(write).not.toHaveBeenCalled();
});

it.each([
  ["missing id", "openapi: 3.1.0\npaths:\n  /items:\n    get: {}", "Every OpenAPI operation must define operationId"],
  ["duplicate id", "paths:\n  /a:\n    get:\n      operationId: same\n  /b:\n    post:\n      operationId: same", "OpenAPI operationId values must be unique"],
])("rejects OpenAPI %s", async (_name, content, error) => {
  replaceFile("content/openapi.yaml", () => content);
  await expect(generate()).rejects.toThrow(error);
  expect(write).not.toHaveBeenCalled();
});

it("requires configured documentation groups", async () => {
  vi.doMock("../docs.config", () => ({ docsConfig: docsConfig.filter((group) => group.title !== "Getting Started") }));
  await expect(generate()).rejects.toThrow("Unknown documentation group: Getting Started");
});

it("rejects pages duplicated across sections", async () => {
  const groups = structuredClone(docsConfig);
  groups[1].pages.push(groups[0].pages[0]);
  vi.doMock("../docs.config", () => ({ docsConfig: groups }));
  await expect(generate()).rejects.toThrow("appears in more than one LLM section");
});

it("rejects configured pages without a publication section", async () => {
  vi.doMock("../docs.config", () => ({ docsConfig: [...docsConfig, { title: "Unorganized", pages: [{ slug: "orphan", title: "Orphan", description: "Test", source: "orphan", sourceRefs: [] }] }] }));
  await expect(generate()).rejects.toThrow("Documentation pages are missing from LLM sections: orphan");
});

it("rejects compact-index slugs removed from documentation", async () => {
  const groups = structuredClone(docsConfig);
  for (const group of groups) group.pages = group.pages.filter((page) => page.slug !== "introduction");
  vi.doMock("../docs.config", () => ({ docsConfig: groups }));
  await expect(generate()).rejects.toThrow("Compact LLM index contains unknown pages: introduction");
});

it("strips valid frontmatter and rewrites markdown and HTML links", async () => {
  process.argv = originalArguments;
  replaceFile("content/docs/introduction.mdx", () => '---\ntitle: Internal\n---\n# Published\n[Jobs](/docs/concepts/jobs)\n<a href="/docs/concepts/workflows">Workflows</a>');
  await generate();
  const full = write.mock.calls.find(([name]) => String(name).endsWith("llms-full.txt"))![1] as string;
  expect(full).toContain("# Published");
  expect(full).toContain("[Jobs](./docs/concepts/jobs)");
  expect(full).toContain('href="./docs/concepts/workflows"');
  expect(full).not.toContain("title: Internal");
});

it("rejects compact publication descriptions that exceed the size budget", async () => {
  const groups = structuredClone(docsConfig);
  const page = groups.flatMap((group) => group.pages).find((page) => page.slug === "concepts/jobs")!;
  page.description = "x".repeat(6000);
  vi.doMock("../docs.config", () => ({ docsConfig: groups }));
  await expect(generate()).rejects.toThrow("must be between 1 KB and 5 KB");
  expect(write).not.toHaveBeenCalled();
});

it("rejects page metadata that breaks published document blocks", async () => {
  const groups = structuredClone(docsConfig);
  groups[0].pages[0].title = 'Invalid " title';
  vi.doMock("../docs.config", () => ({ docsConfig: groups }));
  await expect(generate()).rejects.toThrow("must contain every documentation page");
  expect(write).not.toHaveBeenCalled();
});
