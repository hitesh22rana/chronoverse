import fs from "node:fs";
import path from "node:path";

import { parse } from "yaml";

export type OpenApiParameter = {
  name: string;
  in: "path" | "query" | "header" | "cookie";
  required?: boolean;
  description?: string;
  schema?: { type?: string; enum?: string[]; format?: string; default?: unknown };
};

export type OpenApiOperation = {
  operationId: string;
  method: string;
  path: string;
  summary: string;
  description?: string;
  tags: string[];
  parameters: OpenApiParameter[];
  requestBody?: Record<string, unknown>;
  responses: Record<string, { description?: string; content?: Record<string, unknown> }>;
};

type OpenApiDocument = {
  info: { title: string; version: string; description?: string };
  paths: Record<string, Record<string, Record<string, unknown>>>;
  components?: { parameters?: Record<string, OpenApiParameter> };
};

const HTTP_METHODS = new Set(["get", "post", "put", "patch", "delete"]);

export function getOpenApiPath() {
  return path.join(process.cwd(), "content/openapi.yaml");
}

export function getOpenApiDocument() {
  return parse(fs.readFileSync(getOpenApiPath(), "utf8")) as OpenApiDocument;
}

export function getOpenApiOperations(): OpenApiOperation[] {
  const document = getOpenApiDocument();

  function resolveParameter(parameter: OpenApiParameter | { $ref: string }): OpenApiParameter {
    if (!("$ref" in parameter)) return parameter;
    const name = parameter.$ref.split("/").at(-1) ?? "";
    const resolved = document.components?.parameters?.[name];
    if (!resolved) throw new Error(`Unresolved OpenAPI parameter reference: ${parameter.$ref}`);
    return resolved;
  }

  return Object.entries(document.paths).flatMap(([route, pathItem]) =>
    Object.entries(pathItem)
      .filter(([method]) => HTTP_METHODS.has(method))
      .map(([method, value]) => {
        const operation = value as Record<string, unknown>;
        const pathParameters = (pathItem.parameters as unknown as Array<OpenApiParameter | { $ref: string }> | undefined) ?? [];
        const operationParameters = (operation.parameters as Array<OpenApiParameter | { $ref: string }> | undefined) ?? [];
        return {
          operationId: String(operation.operationId),
          method: method.toUpperCase(),
          path: route,
          summary: String(operation.summary ?? operation.operationId),
          description: operation.description ? String(operation.description) : undefined,
          tags: (operation.tags as string[] | undefined) ?? ["API"],
          parameters: [...pathParameters, ...operationParameters].map(resolveParameter),
          requestBody: operation.requestBody as Record<string, unknown> | undefined,
          responses: (operation.responses as OpenApiOperation["responses"] | undefined) ?? {},
        };
      }),
  );
}

export function getOpenApiOperation(operationId: string) {
  return getOpenApiOperations().find((operation) => operation.operationId === operationId);
}
