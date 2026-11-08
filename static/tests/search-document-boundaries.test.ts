// @vitest-environment node
import { afterEach, expect, it, vi } from "vitest";

import { getOpenApiOperations } from "@/lib/openapi";
import { getSearchDocuments } from "@/lib/docs";

vi.mock("@/lib/openapi", async (importOriginal) => {
  const original = await importOriginal<typeof import("@/lib/openapi")>();
  return { ...original, getOpenApiOperations: vi.fn(original.getOpenApiOperations) };
});

afterEach(() => vi.restoreAllMocks());

it("indexes operations without optional descriptions", () => {
  const operation = getOpenApiOperations()[0];
  vi.mocked(getOpenApiOperations).mockReturnValue([{ ...operation, description: undefined }]);
  const entry = getSearchDocuments().find((item) => item.href === `/docs/api/reference/${operation.operationId}`)!;
  expect(entry.text).toBe(` ${operation.tags.join(" ")}`);
  expect(entry.description).toBe(`${operation.method} ${operation.path}`);
});
