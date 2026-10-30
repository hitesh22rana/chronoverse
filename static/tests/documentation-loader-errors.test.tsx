// @vitest-environment node
import { expect, it, vi } from "vitest";

vi.mock("next/navigation", () => ({ notFound: () => { throw new Error("not-found"); } }));
vi.mock("../docs.config", async (importOriginal) => {
  const original = await importOriginal<typeof import("../docs.config")>();
  return { ...original, getDocPage: (slug: string) => {
    const page = original.getDocPage(slug);
    return page && slug === "introduction" ? { ...page, source: "newly-configured-source" } : page;
  } };
});

it("returns not-found when a configured document has no registered content loader", async () => {
  const { default: DocumentationPage } = await import("@/app/docs/[...slug]/page");
  await expect(DocumentationPage({ params: Promise.resolve({ slug: ["introduction"] }) })).rejects.toThrow("not-found");
});
