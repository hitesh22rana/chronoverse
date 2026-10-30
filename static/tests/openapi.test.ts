// @vitest-environment node
import fs from "node:fs";
import { afterEach, describe, expect, it, vi } from "vitest";
import { stringify } from "yaml";
import { getOpenApiDocument, getOpenApiOperation, getOpenApiOperations, getOpenApiPath, OPENAPI_HTTP_METHODS } from "@/lib/openapi";

function document(paths: Record<string, unknown>, extra: Record<string, unknown> = {}) {
  const value = { info: { title: "Fixture", version: "1" }, paths, ...extra };
  vi.spyOn(fs, "readFileSync").mockReturnValue(stringify(value));
  return value;
}

afterEach(() => vi.restoreAllMocks());

describe("OpenAPI document resolution", () => {
  it("loads the repository document with a cwd-relative source path", () => {
    expect(getOpenApiPath()).toBe(`${process.cwd()}/content/openapi.yaml`);
    expect(getOpenApiDocument().info.title).toContain("Chronoverse");
    const operations = getOpenApiOperations();
    expect(operations.length).toBeGreaterThan(10);
    expect(getOpenApiOperation(operations[0].operationId)).toEqual(operations[0]);
    expect(getOpenApiOperation("absent-operation")).toBeUndefined();
  });

  it("keeps supported methods and supplies public operation defaults", () => {
    document({ "/items": Object.fromEntries([
      ["summary", "Not an operation"],
      ...OPENAPI_HTTP_METHODS.map((method) => [method, { operationId: method }]),
    ]) });
    expect(getOpenApiOperations()).toEqual(OPENAPI_HTTP_METHODS.map((method) => ({
      operationId: method, method: method.toUpperCase(), path: "/items", summary: method,
      description: undefined, tags: ["API"], security: [], securitySchemes: {}, parameters: [],
      requestBody: undefined, responses: {},
    })));
  });

  it("merges path parameters, resolves nested references and preserves sibling overrides", () => {
    document({ "/items/{id}": {
      parameters: [{ $ref: "#/components/parameters/id" }],
      post: { operationId: "create", summary: "Create item", description: "Details", tags: ["Items"],
        parameters: [{ name: "q", in: "query", schema: { type: "string", default: "" } }],
        requestBody: { $ref: "#/components/requestBodies/item", required: true },
        responses: { "201": { $ref: "#/components/responses/created", description: "Override" } },
      },
    } }, { components: {
      parameters: { id: { name: "id", in: "path", required: true, schema: { type: "integer" } } },
      requestBodies: { item: { required: false, content: { "application/json": { schema: { $ref: "#/components/schemas/with~1slash~0tilde" } } } } },
      schemas: { "with/slash~tilde": { type: "object", required: ["enabled"], properties: { enabled: { type: "boolean", default: false }, choices: { enum: [0, false, null] } } } },
      responses: { created: { description: "Created", content: { "application/json": { schema: { type: "array", items: { $ref: "#/components/schemas/with~1slash~0tilde" } } } } } },
    } });
    const operation = getOpenApiOperation("create")!;
    expect(operation.parameters.map((p) => p.name)).toEqual(["id", "q"]);
    expect(operation.requestBody?.required).toBe(true);
    const schema = operation.requestBody?.content?.["application/json"].schema;
    expect(schema?.properties?.enabled.default).toBe(false);
    expect(schema?.properties?.choices.enum).toEqual([0, false, null]);
    expect(operation.responses["201"].description).toBe("Override");
    expect(operation.responses["201"].content?.["application/json"].schema?.items).toEqual(schema);
  });

  it("inherits global security while allowing explicit public and alternative requirements", () => {
    document({ "/private": { get: { operationId: "private" } }, "/public": { get: { operationId: "public", security: [] } },
      "/scoped": { get: { operationId: "scoped", security: [{ token: ["read"] }, { cookie: [] }] } } }, {
      security: [{ token: [] }], components: { securitySchemes: {
        token: { type: "http", scheme: "bearer" }, cookie: { type: "apiKey", in: "cookie", name: "session" },
      } },
    });
    expect(getOpenApiOperation("private")?.securitySchemes).toEqual({ token: { type: "http", scheme: "bearer" } });
    expect(getOpenApiOperation("public")?.securitySchemes).toEqual({});
    expect(getOpenApiOperation("scoped")?.security).toEqual([{ token: ["read"] }, { cookie: [] }]);
  });

  it.each([
    ["https://example.com/schema", {}, "Unsupported external OpenAPI reference"],
    ["#/components/schemas/missing", {}, "Unresolved OpenAPI reference"],
    ["#/components/schemas/value", { value: 3 }, "OpenAPI reference is not an object"],
    ["#/components/schemas/value", { value: { $ref: "#/components/schemas/value" } }, "Circular OpenAPI reference"],
  ])("rejects invalid reference %s", (reference, schemas, message) => {
    document({ "/items": { get: { operationId: "get", responses: { "200": { $ref: reference } } } } }, { components: { schemas } });
    expect(() => getOpenApiOperations()).toThrow(message);
  });

  it.each([undefined, { securitySchemes: {} }])("rejects unresolved security scheme", (components) => {
    document({ "/items": { get: { operationId: "get", security: [{ absent: [] }] } } }, { components });
    expect(() => getOpenApiOperations()).toThrow("Unresolved OpenAPI security scheme: absent");
  });
});
