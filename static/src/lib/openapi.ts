import fs from "node:fs";
import path from "node:path";

import { parse } from "yaml";

export type OpenApiParameter = {
  name: string;
  in: "path" | "query" | "header" | "cookie";
  required?: boolean;
  description?: string;
  schema?: OpenApiSchema;
};

export type OpenApiSchema = {
  type?: string;
  format?: string;
  description?: string;
  enum?: unknown[];
  default?: unknown;
  minimum?: number;
  maximum?: number;
  minLength?: number;
  maxLength?: number;
  minItems?: number;
  maxItems?: number;
  uniqueItems?: boolean;
  required?: string[];
  properties?: Record<string, OpenApiSchema>;
  items?: OpenApiSchema;
  allOf?: OpenApiSchema[];
  anyOf?: OpenApiSchema[];
  oneOf?: OpenApiSchema[];
  additionalProperties?: boolean | OpenApiSchema;
  [key: string]: unknown;
};

export type OpenApiContent = Record<string, { schema?: OpenApiSchema }>;

export type OpenApiRequestBody = {
  required?: boolean;
  description?: string;
  content?: OpenApiContent;
};

export type OpenApiSecurityScheme = {
  type: string;
  description?: string;
  in?: "query" | "header" | "cookie";
  name?: string;
  scheme?: string;
  bearerFormat?: string;
};

export type OpenApiSecurityRequirement = Record<string, string[]>;

export type OpenApiOperation = {
  operationId: string;
  method: string;
  path: string;
  summary: string;
  description?: string;
  tags: string[];
  security: OpenApiSecurityRequirement[];
  securitySchemes: Record<string, OpenApiSecurityScheme>;
  parameters: OpenApiParameter[];
  requestBody?: OpenApiRequestBody;
  responses: Record<string, OpenApiResponse>;
};

export type OpenApiResponse = {
  description?: string;
  content?: OpenApiContent;
};

type OpenApiDocument = {
  info: { title: string; version: string; description?: string };
  paths: Record<string, Record<string, Record<string, unknown>>>;
  security?: OpenApiSecurityRequirement[];
  components?: {
    securitySchemes?: Record<string, OpenApiSecurityScheme>;
    parameters?: Record<string, OpenApiParameter>;
    responses?: Record<string, OpenApiResponse>;
    schemas?: Record<string, OpenApiSchema>;
    requestBodies?: Record<string, OpenApiRequestBody>;
  };
};

const HTTP_METHODS = new Set(["get", "post", "put", "patch", "delete"]);

export function getOpenApiPath() {
  return path.join(process.cwd(), "content/openapi.yaml");
}

export function getOpenApiDocument() {
  return parse(fs.readFileSync(getOpenApiPath(), "utf8")) as OpenApiDocument;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function resolveJsonPointer(document: OpenApiDocument, reference: string): unknown {
  if (!reference.startsWith("#/")) {
    throw new Error(`Unsupported external OpenAPI reference: ${reference}`);
  }

  return reference
    .slice(2)
    .split("/")
    .map((segment) => segment.replaceAll("~1", "/").replaceAll("~0", "~"))
    .reduce<unknown>((value, segment) => {
      if (!isRecord(value) || !(segment in value)) {
        throw new Error(`Unresolved OpenAPI reference: ${reference}`);
      }
      return value[segment];
    }, document);
}

function dereferenceValue(value: unknown, document: OpenApiDocument, references = new Set<string>()): unknown {
  if (Array.isArray(value)) {
    return value.map((item) => dereferenceValue(item, document, new Set(references)));
  }
  if (!isRecord(value)) return value;

  if (typeof value.$ref === "string") {
    if (references.has(value.$ref)) {
      throw new Error(`Circular OpenAPI reference: ${value.$ref}`);
    }
    const nextReferences = new Set(references).add(value.$ref);
    const resolved = resolveJsonPointer(document, value.$ref);
    if (!isRecord(resolved)) throw new Error(`OpenAPI reference is not an object: ${value.$ref}`);
    const siblings = { ...value };
    delete siblings.$ref;
    return dereferenceValue({ ...resolved, ...siblings }, document, nextReferences);
  }

  return Object.fromEntries(
    Object.entries(value).map(([key, child]) => [key, dereferenceValue(child, document, new Set(references))]),
  );
}

export function getOpenApiOperations(): OpenApiOperation[] {
  const document = getOpenApiDocument();

  return Object.entries(document.paths).flatMap(([route, pathItem]) =>
    Object.entries(pathItem)
      .filter(([method]) => HTTP_METHODS.has(method))
      .map(([method, value]) => {
        const operation = value as Record<string, unknown>;
        const pathParameters = (pathItem.parameters as unknown as Array<OpenApiParameter | { $ref: string }> | undefined) ?? [];
        const operationParameters = (operation.parameters as Array<OpenApiParameter | { $ref: string }> | undefined) ?? [];
        const security = (operation.security as OpenApiSecurityRequirement[] | undefined) ?? document.security ?? [];
        const securitySchemeNames = new Set(security.flatMap((requirement) => Object.keys(requirement)));
        const securitySchemes = Object.fromEntries(
          [...securitySchemeNames].map((name) => {
            const scheme = document.components?.securitySchemes?.[name];
            if (!scheme) throw new Error(`Unresolved OpenAPI security scheme: ${name}`);
            return [name, dereferenceValue(scheme, document) as OpenApiSecurityScheme];
          }),
        );
        return {
          operationId: String(operation.operationId),
          method: method.toUpperCase(),
          path: route,
          summary: String(operation.summary ?? operation.operationId),
          description: operation.description ? String(operation.description) : undefined,
          tags: (operation.tags as string[] | undefined) ?? ["API"],
          security,
          securitySchemes,
          parameters: [...pathParameters, ...operationParameters].map((parameter) =>
            dereferenceValue(parameter, document),
          ) as OpenApiParameter[],
          requestBody: operation.requestBody
            ? (dereferenceValue(operation.requestBody, document) as OpenApiRequestBody)
            : undefined,
          responses: Object.fromEntries(
            Object.entries(
              (operation.responses as Record<string, OpenApiResponse | { $ref: string }> | undefined) ?? {},
            ).map(([status, response]) => [status, dereferenceValue(response, document) as OpenApiResponse]),
          ),
        };
      }),
  );
}

export function getOpenApiOperation(operationId: string) {
  return getOpenApiOperations().find((operation) => operation.operationId === operationId);
}
