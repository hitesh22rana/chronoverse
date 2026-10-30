// @vitest-environment node
import fs from "node:fs";
import path from "node:path";
import { afterEach, beforeEach, expect, it, vi } from "vitest";

beforeEach(() => {
  vi.resetModules();
  vi.spyOn(console, "log").mockImplementation(() => {});
  vi.spyOn(console, "error").mockImplementation(() => {});
  vi.spyOn(process, "exit").mockImplementation((code) => { throw new Error(`exit:${code}`); });
});
afterEach(() => {
  for (const name of ["node:fs", "../docs.config", "@/lib/docs", "@/lib/openapi", "@apidevtools/swagger-parser"]) vi.doUnmock(name);
  vi.restoreAllMocks();
});
const validate = () => import("../scripts/validate-docs");

it("validates actual registered MDX sources and canonical OpenAPI", async () => {
  await validate();
  expect(console.log).toHaveBeenCalledWith(expect.stringMatching(/^Validated \d+ MDX pages and \d+ OpenAPI operations\.$/));
  expect(process.exit).not.toHaveBeenCalled();
});

it("reports metadata files link and security defects together", async () => {
  const content = path.join(process.cwd(), "content/docs");
  const rootPage = path.join(content, "page.mdx");
  const missing = path.join(content, "missing.mdx");
  const orphan = path.join(content, "nested/orphan.mdx");
  const page = { slug: "page", title: "Page", description: "Contract", source: "page", sourceRefs: ["missing-repo-source"] };
  vi.doMock("../docs.config", () => ({ docPages: [page, { ...page, sourceRefs: [] }, { slug: "", title: "", description: "", source: "missing", sourceRefs: [] }] }));
  vi.doMock("node:fs", () => ({ default: { ...fs,
    existsSync: (name: fs.PathLike) => path.resolve(String(name)) === rootPage,
    readdirSync: (directory: fs.PathLike) => path.resolve(String(directory)) === content
      ? [{ name: "page.mdx", isDirectory: () => false }, { name: "nested", isDirectory: () => true }, { name: "notes.txt", isDirectory: () => false }]
      : [{ name: "orphan.mdx", isDirectory: () => false }],
  } }));
  vi.doMock("@/lib/docs", () => ({ getDocSourcePath: (name: string) => name === "page" ? rootPage : missing, getDocSource: () => "[Broken](/docs/unknown)\n[Valid](/docs/page#heading)" }));
  vi.doMock("@apidevtools/swagger-parser", () => ({ default: { validate: vi.fn().mockRejectedValue(new Error("invalid schema")) } }));
  vi.doMock("@/lib/openapi", () => ({ getOpenApiPath: () => "fixture.yaml", getOpenApiOperations: () => [
    { operationId: "registerUser", security: [{ unknown: [] }], securitySchemes: {}, responses: { "200": {} }, nested: [{ $ref: "#/unresolved" }] },
    { operationId: "logoutUser", security: [], securitySchemes: {}, responses: {} },
    { operationId: "customOperation", security: [{ cookieSession: [], csrfToken: [], api: [] }], securitySchemes: { cookieSession: { description: "Session" }, csrfToken: { description: "CSRF" }, api: { type: "apiKey" } }, responses: {} },
  ] }));
  await expect(validate()).rejects.toThrow("exit:1");
  const message = vi.mocked(console.error).mock.calls.flat().join("\n");
  for (const expected of ["Malformed documentation metadata", "Duplicate documentation slug: page", "Missing MDX source", "Missing repository source reference", `Unregistered MDX file: ${path.relative(content, orphan)}`, "Invalid OpenAPI document", "Unresolved OpenAPI reference", "Public operation unexpectedly requires authentication", "Protected operation is missing cookieSession", "incorrect csrfToken security", "Missing generated security scheme", "Missing security scheme description", "Incomplete apiKey security scheme", "Missing response description", "Broken internal docs link"]) expect(message).toContain(expected);
  expect(console.log).not.toHaveBeenCalled();
});
