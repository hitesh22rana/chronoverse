import { afterEach, describe, expect, it } from "vitest";
import { cleanup, render, screen, within } from "@testing-library/react";
import { ApiOperation } from "@/components/docs/api-operation";
import { JsonLd } from "@/components/seo/json-ld";
import { ArchitectureMap, Callout, CardGrid, DocCard, Endpoint, SourceLink, Step, Steps } from "@/components/docs/mdx-blocks";
import { architectureMapContent } from "@/lib/architecture";
import { REPOSITORY_URL } from "@/lib/site";
import type { OpenApiOperation, OpenApiSchema } from "@/lib/openapi";

afterEach(cleanup);
function operation(overrides: Partial<OpenApiOperation> = {}): OpenApiOperation {
  return { operationId: "create", method: "POST", path: "/items", summary: "Create item", tags: [], security: [], securitySchemes: {}, parameters: [], responses: {}, ...overrides };
}

describe("API operation rendering", () => {
  it("describes public operations with no parameters or request body", () => {
    const { container } = render(<ApiOperation operation={operation({ responses: { "204": {} } })} />);
    expect(screen.getByText("No authentication required.")).toBeTruthy();
    expect(screen.getByText("This operation has no path, query, header, or cookie parameters.")).toBeTruthy();
    expect(screen.getByText("Response")).toBeTruthy();
    expect(container.querySelector("#request-body")).toBeNull();
  });

  it("shows grouped credentials as AND and alternatives as OR with explicit scope/location", () => {
    render(<ApiOperation operation={operation({ description: "Create contract", security: [{ bearer: ["read", "write"], csrf: [] }, { custom: [] }], securitySchemes: {
      bearer: { type: "http", scheme: "bearer", description: "Bearer credential" },
      csrf: { type: "apiKey", in: "header", name: "X-CSRF" }, custom: { type: "mutualTLS" },
    } })} />);
    for (const text of ["Create contract", "Authentication option 1", "Authentication option 2", "Bearer credential", "scopes: read | write", "header: X-CSRF", "All credentials in this group are required.", "Use any one authentication option shown above."]) expect(screen.getByText(text)).toBeTruthy();
    expect(screen.getAllByText("Authentication credential required by this operation.")).toHaveLength(2);
  });

  it.each([
    [undefined, ["string"]],
    [{}, ["any"]],
    [{ properties: {} }, ["object"]],
    [{ items: {} }, ["array"]],
    [{ type: "integer", format: "int64", enum: ["a", 0, false, { x: 1 }], default: false, minimum: 0, maximum: 10, minLength: 0, maxLength: 2, minItems: 0, maxItems: 3, uniqueItems: true }, ["integer · int64", 'enum: a | 0 | false | {"x":1}', "default: false", "min: 0", "max: 10", "min length: 0", "max length: 2", "min items: 0", "max items: 3", "unique items"]],
  ] satisfies Array<[OpenApiSchema | undefined, string[]]>)('renders parameter constraints %#', (schema, expected) => {
    render(<ApiOperation operation={operation({ parameters: [{ name: "query", in: "query", schema, required: true, description: "Find item" }] })} />);
    const constraints = screen.getByLabelText("query constraints");
    expect(Array.from(constraints.querySelectorAll("code"), (node) => node.textContent)).toEqual(expected);
    expect(screen.getByText("Find item")).toBeTruthy();
    expect(screen.getByText("required")).toBeTruthy();
  });

  it("renders request/response schemas and distinguishes absent schemas", () => {
    render(<ApiOperation operation={operation({ parameters: [{ name: "optional", in: "header" }], requestBody: { required: true, description: "Payload", content: { "application/json": { schema: { type: "object" } }, "text/plain": {} } }, responses: { "200": { description: "Accepted", content: { "application/json": { schema: { type: "boolean" } } } } } })} />);
    expect(screen.getByText("No additional description.")).toBeTruthy();
    expect(screen.getByText("Payload")).toBeTruthy();
    expect(screen.getByText("No response schema.")).toBeTruthy();
    expect(screen.getByText("Accepted")).toBeTruthy();
    expect(Array.from(document.querySelectorAll("pre code"), (node) => JSON.parse(node.textContent!))).toContainEqual({ type: "object" });
  });

  it("distinguishes an explicitly declared body without content", () => {
    render(<ApiOperation operation={operation({ requestBody: {} })} />);
    expect(screen.getByRole("heading", { name: "Request body" })).toBeTruthy();
    expect(screen.getByText("No request body schema.")).toBeTruthy();
  });
});

it("serializes JSON-LD without allowing script termination or HTML entities", () => {
  const value = { text: '</script><script>alert("x")</script>&' };
  const { container } = render(<JsonLd data={value} />);
  const script = container.querySelector('script[type="application/ld+json"]')!;
  expect(script.textContent).not.toContain("<");
  expect(script.textContent).not.toContain("&");
  expect(container.querySelectorAll("script")).toHaveLength(1);
  expect(JSON.parse(script.textContent!)).toEqual(value);
});

it.each(["info", "note", "warning"] as const)("renders %s callouts with accessible content", (type) => {
  render(<Callout type={type} title="Contract">Do this</Callout>);
  const alert = screen.getByRole("alert");
  expect(within(alert).getByText("Contract")).toBeTruthy();
  expect(within(alert).getByText("Do this")).toBeTruthy();
  expect(alert.className.includes("border-destructive/50")).toBe(type === "warning");
});

it("renders documentation links, steps and actual architecture content", () => {
  render(<><Callout title="Default">Information</Callout><CardGrid><DocCard href="/docs/quickstart" title="Start">Setup</DocCard></CardGrid><Steps><Step title="Install">Run it</Step></Steps><Endpoint method="POST" path="/items">Payload</Endpoint><SourceLink path="internal/server/server.go">Source</SourceLink><ArchitectureMap /></>);
  expect(screen.getByRole("link", { name: "Start" }).getAttribute("href")).toBe("/docs/quickstart");
  const source = screen.getByRole("link", { name: /Source.*internal\/server/ });
  expect(source.getAttribute("href")).toBe(`${REPOSITORY_URL}/blob/main/internal/server/server.go`);
  expect(source.getAttribute("rel")).toBe("noopener noreferrer");
  expect(screen.getByRole("listitem").textContent).toContain("InstallRun it");
  expect(screen.getByText("/items")).toBeTruthy();
  for (const stage of architectureMapContent.stages) expect(screen.getByText(stage.label)).toBeTruthy();
  expect(screen.getByText(architectureMapContent.caption)).toBeTruthy();
});
