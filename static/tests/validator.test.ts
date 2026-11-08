// @vitest-environment node
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { stringify } from "yaml";
import SwaggerParser from "@apidevtools/swagger-parser";

beforeEach(() => {
  vi.resetModules();
  vi.spyOn(console, "log").mockImplementation(() => {});
  vi.spyOn(console, "error").mockImplementation(() => {});
  vi.spyOn(process, "exit").mockImplementation((code) => { throw new Error(`exit:${code}`); });
});
afterEach(() => {
  for (const name of ["node:fs", "../docs.config", "@apidevtools/swagger-parser"]) vi.doUnmock(name);
  vi.restoreAllMocks();
});
const validate = () => import("../scripts/validate-docs");

function fixtureReads(contents: Record<string, string>) {
  return (...args: Parameters<typeof fs.readFileSync>) => {
    const content = contents[path.resolve(String(args[0]))];
    return content === undefined ? fs.readFileSync(...args) : content;
  };
}

it("validates actual registered MDX sources and canonical OpenAPI", async () => {
  await validate();
  expect(console.log).toHaveBeenCalledWith(expect.stringMatching(/^Validated \d+ MDX pages and \d+ OpenAPI operations\.$/));
  expect(process.exit).not.toHaveBeenCalled();
});

it("reports metadata file link and parsed security defects together", async () => {
  const content = path.join(process.cwd(), "content/docs");
  const rootPage = path.join(content, "page.mdx");
  const orphan = path.join(content, "nested/orphan.mdx");
  const specification = path.join(process.cwd(), "content/openapi.yaml");
  const page = { slug: "page", title: "Page", description: "Contract", source: "page", sourceRefs: ["missing-repo-source"] };
  const document = {
    openapi: "3.1.0", info: { title: "Policy defects", version: "1.0.0" },
    paths: {
      "/register": { post: { operationId: "registerUser", security: [{ api: [] }], responses: { "200": {} } } },
      "/logout": { post: { operationId: "logoutUser", security: [], responses: {} } },
      "/custom": { get: { operationId: "customOperation", security: [{ cookieSession: [], csrfToken: [], api: [] }], responses: {} } },
    },
    components: { securitySchemes: {
      cookieSession: { type: "apiKey", description: "Session" },
      csrfToken: { type: "apiKey", description: "CSRF" },
      api: { type: "apiKey" },
    } },
  };
  vi.doMock("../docs.config", async (importOriginal) => ({
    ...await importOriginal<typeof import("../docs.config")>(),
    docPages: [page, { ...page, sourceRefs: [] }, { slug: "", title: "", description: "", source: "missing", sourceRefs: [] }],
  }));
  vi.doMock("node:fs", () => ({ default: { ...fs,
    readFileSync: fixtureReads({ [specification]: stringify(document), [rootPage]: "[Broken](/docs/unknown)\n[Valid](/docs/page#heading)" }),
    existsSync: (name: fs.PathLike) => path.resolve(String(name)) === rootPage,
    readdirSync: (directory: fs.PathLike) => path.resolve(String(directory)) === content
      ? [{ name: "page.mdx", isDirectory: () => false }, { name: "nested", isDirectory: () => true }, { name: "notes.txt", isDirectory: () => false }]
      : [{ name: "orphan.mdx", isDirectory: () => false }],
  } }));
  vi.doMock("@apidevtools/swagger-parser", () => ({ default: { validate: vi.fn().mockRejectedValue(new Error("invalid schema")) } }));
  await expect(validate()).rejects.toThrow("exit:1");
  const message = vi.mocked(console.error).mock.calls.flat().join("\n");
  for (const expected of ["Malformed documentation metadata", "Duplicate documentation slug: page", "Missing MDX source", "Missing repository source reference", `Unregistered MDX file: ${path.relative(content, orphan)}`, "Invalid OpenAPI document", "Public operation unexpectedly requires authentication", "Protected operation is missing cookieSession", "incorrect csrfToken security", "Missing security scheme description", "Incomplete apiKey security scheme", "Missing response description", "Broken internal docs link"]) expect(message).toContain(expected);
  expect(console.log).not.toHaveBeenCalled();
});

it.each([
  [{ responses: { "200": { $ref: "#/components/responses/missing" } } }, "Unresolved OpenAPI reference"],
  [{ security: [{ missing: [] }] }, "Unresolved OpenAPI security scheme: missing"],
])("propagates an actual OpenAPI parser failure before reporting success %#", async (operation, message) => {
  const specification = path.join(process.cwd(), "content/openapi.yaml");
  const document = {
    openapi: "3.1.0", info: { title: "Invalid reference", version: "1.0.0" },
    paths: { "/items": { get: { operationId: "items", ...operation } } },
  };
  vi.doMock("node:fs", () => ({ default: { ...fs, readFileSync: fixtureReads({ [specification]: stringify(document) }) } }));
  vi.doMock("@apidevtools/swagger-parser", () => ({ default: { validate: vi.fn().mockRejectedValue(new Error("invalid schema")) } }));
  await expect(validate()).rejects.toThrow(message);
  expect(console.log).not.toHaveBeenCalled();
});

it("reports parser failures and references retained in malformed operation metadata", async () => {
  const directory = fs.mkdtempSync(path.join(os.tmpdir(), "chronoverse-openapi-"));
  try {
    const specification = path.join(directory, "openapi.yaml");
    const source = stringify({
      openapi: "3.1.0", info: { title: "Malformed tags", version: "1.0.0" },
      paths: { "/items": { get: {
        operationId: "listItems", tags: [{ $ref: "#/components/schemas/Missing" }],
        security: [{ cookieSession: [] }], responses: { "200": { description: "Listed" } },
      } } },
      components: { securitySchemes: {
        cookieSession: { type: "apiKey", in: "cookie", name: "session", description: "Session cookie" },
      } },
    });
    fs.writeFileSync(specification, source);
    vi.doMock("node:fs", () => ({ default: { ...fs, readFileSync: fixtureReads({
      [path.join(process.cwd(), "content/openapi.yaml")]: source,
    }) } }));
    vi.doMock("@apidevtools/swagger-parser", () => ({
      default: { validate: () => SwaggerParser.validate(specification) },
    }));
    await expect(validate()).rejects.toThrow("exit:1");
    const message = vi.mocked(console.error).mock.calls.flat().join("\n");
    expect(message).toContain("Invalid OpenAPI document:");
    expect(message).toContain("Unresolved OpenAPI reference in generated operation listItems");
    expect(console.log).not.toHaveBeenCalled();
  } finally {
    fs.rmSync(directory, { recursive: true, force: true });
  }
});
